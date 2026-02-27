package signalling

import (
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sfu"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
)

const (
	PlayerSendPeerStatusInterval = time.Second * 5
)

type Server struct {
	app             *fiber.App
	config          *config.AppConfig
	storage         *Storage
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool
	sfu             sfu.SFU
}

func NewServer(cfg *config.AppConfig, app *fiber.App) (*Server, error) {

	sfu, err := sfu.NewLocalSFU(&cfg.WebRTC, cfg.Server.PublicIP)
	if err != nil {
		return nil, err
	}

	server := Server{
		config:         cfg,
		app:            app,
		playersSockets: sockets.NewSocketPool(),
		grabberSockets: sockets.NewSocketPool(),
		storage:        NewStorage(),
		sfu:            sfu,
	}
	server.storage.setParticipants(cfg.Security.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	metrics.StartTime.Set(float64(time.Now().Unix()))

	return &server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
	s.sfu.Close()
}

func (s *Server) UpdateConfig(cfg *config.AppConfig) {
	s.config = cfg
	s.storage.setParticipants(cfg.Security.Participants)
	metrics.ConfigReloads.Inc()
	slog.Info("server configuration updated")
}

func (s *Server) CheckPlayerCredential(credentials string) bool {
	return s.config.Security.PlayerCredential == nil || *s.config.Security.PlayerCredential == credentials
}

func (s *Server) SetupWebSocketsAndApi() {
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

func (s *Server) isAdminIpAddr(addrPort string) (bool, error) {
	ip, err := netip.ParseAddrPort(addrPort)

	if err != nil {
		return false, fmt.Errorf("can not parse admin ipaddr, error - %v", err)
	}

	for _, n := range s.config.Security.AdminsRawNetworks {
		if n.Contains(ip.Addr()) {
			return true, nil
		}
	}

	return false, nil
}

func (s *Server) setupPlayerSockets() {
	s.app.Get("/ws/player/admin", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic in /ws/player/admin", "error", err)

				return
			}
		}()

		if !s.checkPlayerAdmissions(c) {
			return
		}

		createSendPeerStatus := func(socket sockets.Socket) func() {
			return func() {
				answer := api.PlayerMessage{
					Event:              api.PlayerMessageEventPeerStatus,
					PeersStatus:        s.storage.getAll(),
					ParticipantsStatus: s.storage.getParticipantsStatus(),
				}
				_ = socket.WriteJSON(answer)
			}
		}

		s.listenPlayerSocket(c, createSendPeerStatus)
	}))

	s.app.Get("/ws/player/play", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic in /ws/player/play", "error", err)

				return
			}
		}()

		if !s.checkPlayerAdmissions(c) {
			return
		}

		createSendPing := func(socket sockets.Socket) func() {
			return func() {
				answer := api.PlayerMessage{
					Event: api.PlayerMessageEventPing,
					Ping:  &api.PingMessage{Timestamp: time.Now().Unix()},
				}
				_ = socket.WriteJSON(answer)
			}
		}

		s.listenPlayerSocket(c, createSendPing)
	}))
}

func (s *Server) checkPlayerAdmissions(c *websocket.Conn) bool {
	var message api.PlayerMessage
	ipAddr := c.NetConn().RemoteAddr().String()
	socketID := sockets.SocketID(ipAddr)
	slog.Debug("trying to connect", "socketID", socketID)

	isAdminIpAddr, err := s.isAdminIpAddr(ipAddr)

	if err != nil {
		slog.Error("can not parse ipaddr", "ipAddr", ipAddr, "error", err)
		return false
	}

	if !isAdminIpAddr {
		slog.Warn("blocking access to the admin panel", "ipAddr", ipAddr)

		message.Event = api.PlayerMessageEventAuthFailed
		accessMessage := "Forbidden. IP address black listed"
		message.AccessMessage = &accessMessage

		if err = c.WriteJSON(&message); err != nil {
			slog.Error("can not send message", "message", message, "error", err)
		}

		return false
	}

	message.Event = api.PlayerMessageEventAuthRequest
	if err := c.WriteJSON(&message); err != nil {
		return false
	}
	slog.Debug("requested auth")

	// check authorisation
	if err := c.ReadJSON(&message); err != nil {
		slog.Debug("disconnected", "socketID", socketID)
		return false
	}

	if message.Event != api.PlayerMessageEventAuth || message.PlayerAuth == nil ||
		!s.CheckPlayerCredential(message.PlayerAuth.Credential) {

		accessMessage := "Forbidden. Incorrect credential"

		_ = c.WriteJSON(api.PlayerMessage{
			Event:         api.PlayerMessageEventAuthFailed,
			AccessMessage: &accessMessage,
		})

		slog.Warn("failed to authorize", "socketID", socketID)
		return false
	}

	if err := c.WriteJSON(api.PlayerMessage{
		Event:    api.PlayerMessageEventInitPeer,
		InitPeer: &api.PcConfigMessage{PcConfig: s.config.WebRTC.PeerConnectionConfig},
	}); err != nil {
		slog.Error("failed to send init_peer", "socketID", socketID)
	}
	return true
}

func (s *Server) listenPlayerSocket(c *websocket.Conn, createCallback func(socket sockets.Socket) func()) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	slog.Info("authorized", "socketID", socketID)

	metrics.ActivePlayers.Inc()
	metrics.PlayersConnectedTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()
	defer func() {
		metrics.ActivePlayers.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
	}()
	newC := s.playersSockets.AddSocket(c)

	var message api.PlayerMessage
	callback := createCallback(newC)
	callback()
	timer := utils.SetIntervalTimer(PlayerSendPeerStatusInterval, callback)

	defer func() {
		s.playersSockets.CloseSocket(socketID)
		timer.Stop()
		s.sfu.DeleteSubscriber(socketID)
	}()

	for {
		if err := newC.ReadJSON(&message); err != nil {
			slog.Debug("disconnected", "socketID", socketID, "error", err.Error())
			return
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("player_play", "in").Inc()

		answer := s.processPlayerMessage(newC, socketID, message)
		if answer == nil {
			continue
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("player_play", "out").Inc()
		if err := newC.WriteJSON(answer); err != nil {
			slog.Error("failed to send message", "socketID", socketID, "msg", answer)
			return
		}
	}
}

func (s *Server) processPlayerMessage(c sockets.Socket, id sockets.SocketID,
	m api.PlayerMessage) *api.PlayerMessage {
	slog.Debug("player message", "event", m.Event)
	metrics.SignallingMessagesTotal.WithLabelValues(string(m.Event), "in").Inc()
	switch m.Event {
	case api.PlayerMessageEventPong:
		return nil
	case api.PlayerMessageEventOffer:
		if m.Offer == nil {
			return nil
		}
		slog.Debug("playerMessage", "streamType", m.Offer.StreamType)
		var grabberSocketID sockets.SocketID
		if m.Offer.PeerId != nil {
			grabberSocketID = sockets.SocketID(*m.Offer.PeerId)
		} else if m.Offer.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Offer.PeerName); ok {
				slog.Debug("stream type", "streamType", m.Offer.StreamType)
				if !slices.Contains(peer.StreamTypes, api.StreamType(m.Offer.StreamType)) {
					_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
					slog.Warn("no such stream type in grabber", "streamType", m.Offer.StreamType, "peerName", *m.Offer.PeerName)
					return nil
				}
				grabberSocketID = peer.SocketId
			}
		} else {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			slog.Warn("offer missing PeerId or PeerName")
			return nil
		}

		streamType := m.Offer.StreamType

		grabberConn := s.grabberSockets.GetSocket(grabberSocketID)
		if grabberConn == nil {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			return nil
		}

		publisherKey := sfu.PublisherKey(grabberSocketID, streamType)

		subscriberCallback := createSubscriberCallback(m, id, c, streamType, publisherKey)

		publisherCallbacks := sfu.PublisherCallbacks{
			OnOffer:        createOnOfferCallback(grabberConn, streamType),
			OnICECandidate: createOnICECandidateCallback(grabberConn),
		}

		if err := s.sfu.AddSubscriber(id, grabberSocketID, streamType, subscriberCallback, publisherCallbacks); err != nil {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		}
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}
		s.sfu.SubscriberICE(id, m.Ice.Candidate)
	}
	return nil
}

func (s *Server) setupGrabberSockets() {
	s.app.Get("/ws/peers/:name", websocket.New(func(c *websocket.Conn) {
		socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
		peerName := c.Params("name")
		slog.Debug("grabber trying to connect", "socketID", socketID)

		s.storage.addPeer(peerName, socketID)

		s.listenGrabberSocket(c)
	}))
}

func (s *Server) listenGrabberSocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	newC := s.grabberSockets.AddSocket(c)

	metrics.ActiveAgents.Inc()
	metrics.AgentsRegisteredTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()
	defer func() {
		metrics.ActiveAgents.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
	}()

	if err := newC.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: s.config.WebRTC.PeerConnectionConfig},
			PingInterval:    s.config.Server.GrabberPingInterval,
		},
	}); err != nil {
		slog.Error("failed to send init_peer", "socketID", socketID, "error", err)
		return
	}

	var message api.GrabberMessage
	for {
		if err := newC.ReadJSON(&message); err != nil {
			slog.Debug("grabber disconnected", "socketID", socketID, "error", err.Error())
			s.grabberSockets.CloseSocket(socketID)
			s.sfu.DeletePublisher(socketID)
			break
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("grabber", "in").Inc()

		answer := s.processGrabberMessage(socketID, message)
		if answer == nil {
			continue
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("grabber", "out").Inc()
		if err := newC.WriteJSON(answer); err != nil {
			slog.Error("failed to send answer", "answer", answer, "socketID", socketID)
		}
	}
}

func (s *Server) processGrabberMessage(id sockets.SocketID, m api.GrabberMessage) *api.GrabberMessage {
	metrics.SignallingMessagesTotal.WithLabelValues(string(m.Event), "in").Inc()
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
			slog.Warn("invalid IceMessage: missing data")
			return nil
		}
		grabberKey := *m.Ice.PeerId
		s.sfu.AddICECandidatePublisher(grabberKey, m.Ice.Candidate)
	}
	return nil
}
