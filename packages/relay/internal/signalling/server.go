package signalling

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
)

const PlayerSendPeerStatusInterval = time.Second * 5

type Server struct {
	app             *fiber.App
	config          ServerConfig
	storage         *Storage
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool

	peerManager *PeerManager
}

func NewServer(config ServerConfig, app *fiber.App) (*Server, error) {

	peerManager, err := NewPeerManager(config)
	if err != nil {
		return nil, err
	}

	server := Server{
		config:         config,
		app:            app,
		playersSockets: sockets.NewSocketPool(),
		grabberSockets: sockets.NewSocketPool(),
		storage:        NewStorage(),
		peerManager:    peerManager,
	}
	server.storage.setParticipants(config.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	return &server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
	s.peerManager.Close()
}

func (s *Server) CheckPlayerCredential(credentials string) bool {
	return s.config.PlayerCredential == nil || *s.config.PlayerCredential == credentials
}

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
}

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

	defer func() {
		s.playersSockets.CloseSocket(socketID)
		s.peerManager.DeleteSubscriber(socketID)
	}()

	var message api.PlayerMessage

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

func (s *Server) processPlayerMessage(c sockets.Socket, id sockets.SocketID,
	m api.PlayerMessage) *api.PlayerMessage {
	log.Printf("EVENT: %v, STREAM TYPE: %v", m.Event, m.Offer.StreamType)
	switch m.Event {
	case api.PlayerMessageEventOffer:
		if m.Offer == nil {
			return nil
		}
		var grabberSocketID sockets.SocketID
		if m.Offer.PeerId != nil {
			grabberSocketID = sockets.SocketID(*m.Offer.PeerId)
		} else if m.Offer.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Offer.PeerName); ok {
				if !slices.Contains(peer.StreamTypes, api.StreamType(m.Offer.StreamType)) {
					_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
					log.Printf("no such stream type %v in grabber with id %v",
						m.Offer.StreamType, m.Offer.PeerId)
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

		answer := s.peerManager.AddSubscriber(id, grabberSocketID, streamType, c, &m.Offer.Offer, grabberConn)
		_ = c.WriteJSON(answer)
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}
		s.peerManager.SubscriberICE(id, m.Ice.Candidate)
	}
	return nil
}

func (s *Server) setupGrabberSockets() {
	s.app.Get("/ws/peers/:name", websocket.New(func(c *websocket.Conn) {
		socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
		peerName := c.Params("name")
		log.Printf("trying to connect %s", socketID)

		s.storage.addPeer(peerName, socketID)

		s.listenGrabberSocket(c)
	}))
}

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
			s.peerManager.DeletePublisher(socketID)
			break
		}

		// Process incoming messages and send responses if necessary
		answer := s.processGrabberMessage(socketID, message)
		if answer == nil {
			continue
		}
		if err := newC.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

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
		s.peerManager.OfferAnswerPublisher(grabberKey, m.OfferAnswer.Answer)

	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			log.Printf("invalid IceMessage: missing data")
			return nil
		}
		grabberKey := *m.Ice.PeerId
		s.peerManager.AddICECandidatePublisher(grabberKey, m.Ice.Candidate)
	}
	return nil
}
