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

		s.listenPlayerAdminSocket(c)
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

		s.listenPlayerPlaySocket(c)
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

func (s *Server) listenPlayerAdminSocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
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
			slog.Debug("disconnected", "socketID", socketID, "error", err.Error())
			s.playersSockets.CloseSocket(socketID)
			timer.Stop()
			break
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("player_admin", "in").Inc()

		answer := s.processPlayerMessage(newC, socketID, message)
		if answer == nil {
			continue
		}
		if err := newC.WriteJSON(answer); err != nil {
			slog.Error("failed to send message", "socketID", socketID, "answer", answer)
			return
		}
	}
}

func (s *Server) listenPlayerPlaySocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	newC := s.playersSockets.AddSocket(c)
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

	messages := make(chan interface{})
	defer close(messages)

	go func() { // rewrite this
		for msg := range messages {
			if err := newC.WriteJSON(msg); err != nil {
				slog.Error("failed to send message", "socketID", socketID, "msg", msg)
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
			slog.Debug("disconnected", "socketID", socketID, "error", err.Error())
			s.playersSockets.CloseSocket(socketID)
			break
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("player_play", "in").Inc()

		answer := s.processPlayerMessage(newC, socketID, message)
		if answer == nil {
			continue
		}
		metrics.WebSocketMessagesTotal.WithLabelValues("player_play", "out").Inc()
		messages <- answer
	}
}

func (s *Server) processPlayerMessage(c sockets.Socket, id sockets.SocketID,
	m api.PlayerMessage) *api.PlayerMessage {
	slog.Debug("player message", "event", m.Event, "streamType", m.Offer.StreamType)
	metrics.SignallingMessagesTotal.WithLabelValues(string(m.Event), "in").Inc()
	switch m.Event {
	case api.PlayerMessageEventPong:
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
	s.grabberSockets.AddSocket(c)
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
