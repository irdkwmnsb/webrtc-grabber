package signalling

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sfu"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
)

const (
	// PlayerSendPeerStatusInterval defines how often the admin interface receives
	// peer status updates showing which grabbers are online and their connection counts.
	PlayerSendPeerStatusInterval = time.Second * 5
)

// Server is the main HTTP/WebSocket server that handles client connections and routes
// signaling messages between players (subscribers) and grabbers (publishers).
//
// The server provides three main WebSocket endpoints:
//   - /ws/player/admin: Admin dashboard with peer status monitoring (IP-restricted)
//   - /ws/player/play: Player endpoint for subscribing to media streams (IP-restricted)
//   - /ws/peers/:name: Grabber endpoint for publishing media streams (unrestricted)
//
// Architecture:
//   - Uses Fiber web framework for HTTP/WebSocket handling
//   - Maintains separate socket pools for players and grabbers
//   - Delegates WebRTC signaling to PeerManager
//   - Uses Storage for peer health monitoring and status tracking
//   - Implements IP-based access control for admin endpoints
//   - Supports credential-based authentication for players
//
// Security features:
//   - IP whitelist for admin/player endpoints (AdminsRawNetworks)
//   - Optional password authentication (PlayerCredential)
//   - Panic recovery in WebSocket handlers
//   - TLS/WSS support via configuration
//
// The server coordinates between:
//   - WebSocket message handling (this layer)
//   - WebRTC signaling (PeerManager)
//   - Peer tracking and health monitoring (Storage)
type Server struct {
	// app is the Fiber application instance handling HTTP/WebSocket routes
	app *fiber.App

	// config holds server configuration including auth, codecs, and network settings
	config *config.ServerConfig

	// storage tracks active peers and their health status
	storage *Storage

	// oldPeersCleaner is a timer that periodically removes stale peer entries
	oldPeersCleaner utils.IntervalTimer

	// playersSockets manages WebSocket connections for players (subscribers)
	playersSockets *sockets.SocketPool

	// grabberSockets manages WebSocket connections for grabbers (publishers)
	grabberSockets *sockets.SocketPool

	// sfu handles all WebRTC peer connections and signaling
	sfu sfu.SFU
}

// NewServer creates a new Server instance with the given configuration and Fiber app.
// It initializes all components including PeerManager, Storage, and socket pools,
// and starts a background timer to clean up stale peer entries.
//
// Initialization steps:
//  1. Creates PeerManager with configured codecs and ICE servers
//  2. Initializes storage with expected participant names
//  3. Creates socket pools for players and grabbers
//  4. Starts a timer to clean up peers that haven't pinged in 60 seconds
//
// Parameters:
//   - config: Server configuration loaded from conf/config.json
//   - app: Fiber application instance for mounting routes
//
// Returns an error if PeerManager initialization fails (codec registration, etc.).
//
// The returned server must be closed with Close() before shutdown to properly
// clean up resources, stop timers, and close all connections.
//
// Example:
//
//	config, _ := signalling.LoadServerConfig()
//	app := fiber.New()
//	server, err := signalling.NewServer(&config, app)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer server.Close()
//	server.SetupWebSockets()
//	app.Listen(":8080")
func NewServer(config *config.ServerConfig, app *fiber.App) (*Server, error) {

	sfu, err := sfu.NewLocalSFU(config)
	if err != nil {
		return nil, err
	}

	server := Server{
		config:         config,
		app:            app,
		playersSockets: sockets.NewSocketPool(),
		grabberSockets: sockets.NewSocketPool(),
		storage:        NewStorage(),
		sfu:            sfu,
	}
	server.storage.setParticipants(config.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	return &server, nil
}

// Close gracefully shuts down the server and cleans up all resources.
// This method should be called before application exit, typically in a defer statement.
//
// Cleanup steps:
//  1. Stops the old peer cleaner timer
//  2. Closes all player WebSocket connections
//  3. Closes all grabber WebSocket connections
//  4. Closes all WebRTC peer connections via PeerManager
//
// This method is safe to call multiple times and will not panic.
func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
	s.sfu.Close()
}

// CheckPlayerCredential validates player authentication credentials.
// Returns true if authentication is disabled (no credential configured)
// or if the provided credential matches the configured credential.
//
// Parameters:
//   - credentials: The credential string provided by the player
//
// Returns true if authentication succeeds, false otherwise.
//
// This method is used during the player authentication flow to validate
// credentials before allowing access to admin or play endpoints.
func (s *Server) CheckPlayerCredential(credentials string) bool {
	return s.config.PlayerCredential == nil || *s.config.PlayerCredential == credentials
}

// SetupWebSockets configures all WebSocket endpoints on the Fiber application.
// This method must be called after NewServer and before starting the server.
//
// Endpoints configured:
//   - GET /ws/player/admin - Admin dashboard endpoint (requires IP whitelist + auth)
//   - GET /ws/player/play - Player streaming endpoint (requires IP whitelist + auth)
//   - GET /ws/peers/:name - Grabber publishing endpoint (no restrictions)
//
// The method also adds WebSocket upgrade middleware to the /ws path.
//
// This method should be called once during server initialization:
//
//	server.SetupWebSockets()
//	app.Listen(":8080")
func (s *Server) SetupWebSockets() {
	s.app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	s.setupPlayerSockets()
	s.setupGrabberSockets()

	s.setupAdminApi()
	s.setupAgentApi()
}

// isAdminIpAddr checks if the given IP address is allowed to access admin endpoints.
// The address is compared against the AdminsRawNetworks CIDR ranges from configuration.
//
// Parameters:
//   - addrPort: IP address with port in "IP:port" format (e.g., "192.168.1.10:54321")
//
// Returns:
//   - bool: true if the IP is in an allowed network range, false otherwise
//   - error: parsing error if addrPort is not valid "IP:port" format
//
// This method is called during player authentication to enforce IP-based access control.
func (s *Server) isAdminIpAddr(addrPort string) (bool, error) {
	ip, err := netip.ParseAddrPort(addrPort)

	if err != nil {
		return false, fmt.Errorf("can not parse admin ipaddr, error - %v", err)
	}

	for _, n := range s.config.AdminsRawNetworks {
		if n.Contains(ip.Addr()) {
			return true, nil
		}
	}

	return false, nil
}

// setupPlayerSockets configures the player WebSocket endpoints for admin and play.
// Both endpoints require IP whitelist validation and credential authentication.
//
// Routes configured:
//   - GET /ws/player/admin: Dashboard showing all peer statuses, updates every 5 seconds
//   - GET /ws/player/play: Streaming endpoint for watching grabber streams
//
// Each handler includes panic recovery to prevent server crashes from client errors.
func (s *Server) setupPlayerSockets() {
	s.app.Get("/ws/player/admin", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic in /ws/player/admin: %v", err)

				return
			}
		}()

		if !s.checkPlayerAdmissions(c) {
			return
		}

		s.listenPlayerAdminSocket(c)
	}))

	s.app.Get("/ws/player/play", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic in /ws/player/play: %v", err)

				return
			}
		}()

		if !s.checkPlayerAdmissions(c) {
			return
		}

		s.listenPlayerPlaySocket(c)
	}))
}

// checkPlayerAdmissions performs IP whitelist and credential authentication for player endpoints.
// This is a multi-step authentication flow that validates both network access and credentials.
//
// Authentication flow:
//  1. Extract client IP address from WebSocket connection
//  2. Check if IP is in AdminsRawNetworks whitelist
//  3. If IP blocked, send AuthFailed message and return false
//  4. Send AuthRequest message to client
//  5. Wait for Auth response with credentials
//  6. Validate credentials using CheckPlayerCredential
//  7. If valid, send InitPeer with WebRTC configuration
//  8. Return true to allow connection
//
// Parameters:
//   - c: WebSocket connection from the client
//
// Returns true if authentication succeeds, false if it fails at any step.
//
// Failure cases:
//   - IP not in whitelist (sends "IP address black listed" message)
//   - Invalid or missing credentials (sends "Incorrect credential" message)
//   - Network errors during message exchange
//
// This method handles all error responses to the client before returning false.
func (s *Server) checkPlayerAdmissions(c *websocket.Conn) bool {
	var message api.PlayerMessage
	ipAddr := c.NetConn().RemoteAddr().String()
	socketID := sockets.SocketID(ipAddr)
	log.Printf("trying to connect %s", socketID)

	isAdminIpAddr, err := s.isAdminIpAddr(ipAddr)

	if err != nil {
		log.Printf("can not parse ipaddr %s, error - %v", ipAddr, err)
		return false
	}

	if !isAdminIpAddr {
		log.Printf("blocking access to the admin panel for %s", ipAddr)

		message.Event = api.PlayerMessageEventAuthFailed
		accessMessage := "Forbidden. IP address black listed"
		message.AccessMessage = &accessMessage

		if err = c.WriteJSON(&message); err != nil {
			log.Printf("can not send message %v, error - %v", message, err)
		}

		return false
	}

	message.Event = api.PlayerMessageEventAuthRequest
	if err := c.WriteJSON(&message); err != nil {
		return false
	}
	log.Println("Requested auth")

	// check authorisation
	if err := c.ReadJSON(&message); err != nil {
		log.Printf("disconnected %s", socketID)
		return false
	}

	if message.Event != api.PlayerMessageEventAuth || message.PlayerAuth == nil ||
		!s.CheckPlayerCredential(message.PlayerAuth.Credential) {

		accessMessage := "Forbidden. Incorrect credential"

		_ = c.WriteJSON(api.PlayerMessage{
			Event:         api.PlayerMessageEventAuthFailed,
			AccessMessage: &accessMessage,
		})

		log.Printf("failed to authorize %s", socketID)
		return false
	}

	if err := c.WriteJSON(api.PlayerMessage{
		Event:    api.PlayerMessageEventInitPeer,
		InitPeer: &api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
	}); err != nil {
		log.Printf("failed to send init_peer%s", socketID)
	}
	return true
}

// listenPlayerAdminSocket handles the admin dashboard WebSocket connection.
// This endpoint provides real-time monitoring of all grabbers and their status.
//
// Functionality:
//   - Sends peer status updates every 5 seconds (PlayerSendPeerStatusInterval)
//   - Shows all connected grabbers and their connection counts
//   - Shows configured participants and their online/offline status
//   - Processes player messages (though admin typically only monitors)
//
// The connection loop:
//  1. Sends initial peer status immediately
//  2. Starts a timer to send updates every 5 seconds
//  3. Reads messages from client in a loop
//  4. Processes each message via processPlayerMessage
//  5. Sends response if message requires one
//  6. Breaks on read error (disconnect) and cleans up
//
// Parameters:
//   - c: Authenticated WebSocket connection
//
// This method blocks until the connection closes.
func (s *Server) listenPlayerAdminSocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	log.Printf("authorized %s", socketID)
	newC := sockets.NewSocket(c)

	var message api.PlayerMessage
	sendPeerStatus := func() {
		answer := api.PlayerMessage{
			Event:              api.PlayerMessageEventPeerStatus,
			PeersStatus:        s.storage.getAll(),
			ParticipantsStatus: s.storage.getParticipantsStatus(),
		}
		_ = newC.WriteJSON(answer)
	}
	sendPeerStatus()
	timer := utils.SetIntervalTimer(PlayerSendPeerStatusInterval, sendPeerStatus)

	for {
		if err := newC.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			timer.Stop()
			break
		}

		answer := s.processPlayerMessage(newC, socketID, message)
		if answer == nil {
			continue
		}
		if err := newC.WriteJSON(answer); err != nil {
			log.Printf("failed to send message to %s: %v", socketID, answer)
			return
		}
	}
}

// listenPlayerPlaySocket handles the player streaming WebSocket connection.
// This endpoint allows players to subscribe to grabber streams and receive media.
//
// The connection manages:
//   - Outgoing message queue (via channel) to prevent write blocking
//   - Periodic ping messages every 30 seconds to keep connection alive
//   - WebRTC signaling for stream subscription (offer/answer/ICE)
//   - Automatic cleanup on disconnect
//
// Architecture:
//  1. Main goroutine: Reads messages from client in a loop
//  2. Writer goroutine: Sends messages from channel to client
//  3. Ping goroutine: Sends periodic pings to detect dead connections
//
// Message flow:
//   - Client sends Offer to subscribe to a stream
//   - Server creates subscriber peer connection
//   - Server sends Answer back to client
//   - ICE candidates are exchanged
//   - Media flows through WebRTC peer connection
//
// Cleanup on disconnect:
//   - Closes the message channel
//   - Removes socket from pool
//   - Deletes subscriber from PeerManager (closes peer connection)
//
// Parameters:
//   - c: Authenticated WebSocket connection
//
// This method blocks until the connection closes.
func (s *Server) listenPlayerPlaySocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	newC := sockets.NewSocket(c)
	log.Printf("authorized %s", socketID)

	messages := make(chan interface{})
	defer close(messages)

	go func() { // rewrite this
		for msg := range messages {
			if err := newC.WriteJSON(msg); err != nil {
				log.Printf("failed to send message to %s: %v", socketID, msg)
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			pingMsg := api.PlayerMessage{
				Event: api.PlayerMessageEventPing,
				Ping: &api.PingMessage{
					Timestamp: time.Now().Unix(),
				},
			}
			if err := newC.WriteJSON(pingMsg); err != nil {
				return
			}
		}
	}()

	defer func() {
		s.playersSockets.CloseSocket(socketID)
		s.sfu.DeleteSubscriber(socketID)
	}()

	var message api.PlayerMessage

	// Main read loop
	for {
		if err := newC.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			break
		}

		answer := s.processPlayerMessage(newC, socketID, message)
		if answer == nil {
			continue
		}
		messages <- answer
	}
}

// processPlayerMessage handles incoming messages from player clients and routes them appropriately.
// This method processes WebRTC signaling messages (offer, ICE) and control messages (pong).
//
// Supported message types:
//
//   - Pong: Response to ping, logged for connection monitoring
//   - Offer: Player wants to subscribe to a grabber stream
//   - PlayerIce: ICE candidate from player for connection establishment
//
// Offer processing flow:
//  1. Extract grabber identity (by PeerId or PeerName)
//  2. Validate grabber exists and supports requested stream type
//  3. Delegate to PeerManager.AddSubscriber for WebRTC setup
//  4. Return answer or failure to client
//
// Parameters:
//   - c: Socket connection for sending responses
//   - id: Player's socket ID
//   - m: The player message to process
//
// Returns:
//   - *api.PlayerMessage: Response to send to client, or nil if no response needed
//
// The method handles offer messages inline by calling WriteJSON directly,
// so it returns nil for offers. Other message types may return responses.
func (s *Server) processPlayerMessage(c sockets.Socket, id sockets.SocketID,
	m api.PlayerMessage) *api.PlayerMessage {
	log.Printf("EVENT: %v, STREAM TYPE: %v", m.Event, m.Offer.StreamType)
	switch m.Event {
	case api.PlayerMessageEventPong:
		log.Printf("Received pong from player %s", id)
		return nil
	case api.PlayerMessageEventOffer:
		if m.Offer == nil {
			return nil
		}
		var grabberSocketID sockets.SocketID
		if m.Offer.PeerId != nil {
			grabberSocketID = sockets.SocketID(*m.Offer.PeerId)
		} else if m.Offer.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Offer.PeerName); ok {
				log.Println(m.Offer.StreamType)
				if !slices.Contains(peer.StreamTypes, api.StreamType(m.Offer.StreamType)) {
					_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
					log.Printf("no such stream type %v in grabber with peerName %v",
						m.Offer.StreamType, *m.Offer.PeerName)
					return nil
				}
				grabberSocketID = peer.SocketId
			}
		} else {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			log.Printf("offer missing PeerId or PeerName")
			return nil
		}

		streamType := m.Offer.StreamType

		grabberConn := s.grabberSockets.GetSocket(grabberSocketID)
		if grabberConn == nil {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			return nil
		}

		ctx := sfu.CreateNewSubscriberContext(grabberSocketID, streamType, c, &m.Offer.Offer, grabberConn)
		answer := s.sfu.AddSubscriber(id, ctx)
		_ = c.WriteJSON(answer)
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}
		s.sfu.SubscriberICE(id, m.Ice.Candidate)
	}
	return nil
}

// setupGrabberSockets configures the grabber WebSocket endpoint for publishers.
// Grabbers connect to /ws/peers/:name where :name is a human-readable identifier.
//
// Route configured:
//   - GET /ws/peers/:name - Grabber publishing endpoint (no authentication required)
//
// The endpoint:
//   - Extracts grabber name from URL path parameter
//   - Registers the peer in storage for monitoring
//   - Handles WebRTC signaling for stream publication
//
// No IP restrictions or authentication for grabbers allows flexible deployment
// of grabber devices (cameras, screen capture clients, etc.) without complex setup.
func (s *Server) setupGrabberSockets() {
	s.app.Get("/ws/peers/:name", websocket.New(func(c *websocket.Conn) {
		socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
		peerName := c.Params("name")
		log.Printf("trying to connect %s", socketID)

		s.storage.addPeer(peerName, socketID)

		s.listenGrabberSocket(c)
	}))
}

// listenGrabberSocket handles the grabber WebSocket connection lifecycle.
// This endpoint receives media streams from publishers and coordinates with PeerManager.
//
// Connection flow:
//  1. Send InitPeer with WebRTC configuration and ping interval
//  2. Enter message loop to process grabber messages
//  3. Handle Ping, OfferAnswer, and ICE messages
//  4. On disconnect, clean up publisher from PeerManager
//
// Message types handled:
//   - Ping: Grabber sends periodic status updates (connection count, stream types)
//   - OfferAnswer: Grabber responds to server's offer for stream publication
//   - GrabberIce: ICE candidates from grabber for WebRTC connection
//
// The grabber connection is typically long-lived, staying open as long as
// the grabber is streaming. Multiple subscribers can connect to the same
// grabber without affecting this connection.
//
// Cleanup on disconnect:
//   - Removes socket from pool
//   - Calls PeerManager.DeletePublisher (closes all related subscribers)
//
// Parameters:
//   - c: WebSocket connection from the grabber
//
// This method blocks until the connection closes.
func (s *Server) listenGrabberSocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.grabberSockets.AddSocket(c)
	newC := sockets.NewSocket(c)

	if err := newC.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
			PingInterval:    s.config.GrabberPingInterval,
		},
	}); err != nil {
		log.Printf("failed to send init_peer for %s: %v", socketID, err)
		return
	}

	var message api.GrabberMessage
	for {
		if err := newC.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.grabberSockets.CloseSocket(socketID)
			s.sfu.DeletePublisher(socketID)
			break
		}

		answer := s.processGrabberMessage(socketID, message)
		if answer == nil {
			continue
		}
		if err := newC.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

// processGrabberMessage handles incoming messages from grabber clients and routes them appropriately.
// This method processes periodic pings and WebRTC signaling messages from publishers.
//
// Supported message types:
//
//   - Ping: Periodic health check with connection status (updates Storage)
//   - OfferAnswer: Grabber's answer to server's offer (completes WebRTC handshake)
//   - GrabberIce: ICE candidate from grabber (for connection establishment)
//
// Ping handling:
//   - Updates peer's LastPing timestamp in storage
//   - Updates ConnectionsCount and StreamTypes
//   - Allows admin dashboard to show real-time grabber status
//
// OfferAnswer handling:
//   - Forwards answer to PeerManager to complete publisher setup
//   - Identified by PeerId (publisherKey format: "socketID_streamType")
//
// GrabberIce handling:
//   - Forwards ICE candidate to PeerManager for publisher connection
//   - Required for NAT traversal and connection establishment
//
// Parameters:
//   - id: Grabber's socket ID
//   - m: The grabber message to process
//
// Returns:
//   - *api.GrabberMessage: Response to send to grabber, or nil if no response needed
//
// Currently, no messages require responses (all are one-way notifications),
// so this method always returns nil.
func (s *Server) processGrabberMessage(id sockets.SocketID, m api.GrabberMessage) *api.GrabberMessage {
	switch m.Event {
	case api.GrabberMessageEventPing:
		if m.Ping == nil {
			return nil
		}
		s.storage.ping(id, *m.Ping)
	case api.GrabberMessageEventOfferAnswer:
		if m.OfferAnswer == nil || m.OfferAnswer.PeerId == "" {
			return nil
		}
		grabberKey := m.OfferAnswer.PeerId
		s.sfu.OfferAnswerPublisher(grabberKey, m.OfferAnswer.Answer)

	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			log.Printf("invalid IceMessage: missing data")
			return nil
		}
		grabberKey := *m.Ice.PeerId
		s.sfu.AddICECandidatePublisher(grabberKey, m.Ice.Candidate)
	}
	return nil
}
