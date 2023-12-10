package signalling

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"log"
	"net/netip"
	"os"
	"time"
)

const PlayerSendPeerStatusInterval = time.Second * 5

type Server struct {
	app             *fiber.App
	config          ServerConfig
	storage         *Storage
	adminsNetworks  []netip.Prefix
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool
}

type ServerConfig struct {
	PlayerCredential     *string                  `json:"adminCredential"`
	Participants         []string                 `json:"participants"`
	AdminsRawNetworks    []string                 `json:"adminsNetworks"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig"`
	GrabberPingInterval  int                      `json:"grabberPingInterval"`
	ServerPort           int                      `json:"serverPort"`
	ServerTLSCrtFile     *string                  `json:"serverTLSCrtFile"`
	ServerTLSKeyFile     *string                  `json:"serverTLSKeyFile"`
}

func NewServer(config ServerConfig, app *fiber.App) (*Server, error) {
	server := Server{
		config:         config,
		app:            app,
		playersSockets: sockets.NewSocketPool(),
		grabberSockets: sockets.NewSocketPool(),
		storage:        NewStorage(),
	}
	server.storage.setParticipants(config.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	adminsNetworks, err := parseAdminsNetworks(server.config.AdminsRawNetworks)

	if err != nil {
		return nil, fmt.Errorf("can not parse admins networks, error - %v", err)
	}

	server.adminsNetworks = adminsNetworks

	return &server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
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

	for _, n := range s.adminsNetworks {
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

		var message api.PlayerMessage

		ipAddr := c.NetConn().RemoteAddr().String()
		isAdminIpAddr, err := s.isAdminIpAddr(ipAddr)

		if err != nil {
			log.Printf("can not parse ipaddr %s, error - %v", ipAddr, err)
			return
		}

		if !isAdminIpAddr {
			log.Printf("blocking access to the admin panel for %s", ipAddr)

			message.Event = api.PlayerMessageEventBlackListed

			if err = c.WriteJSON(&message); err != nil {
				return
			}

			return
		}

		socketID := sockets.SocketID(ipAddr)
		log.Printf("trying to connect %s", socketID)

		message.Event = api.PlayerMessageEventAuthRequest
		if err := c.WriteJSON(&message); err != nil {
			return
		}
		log.Println("Requested auth")

		// check authorisation
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s", socketID)
			return
		}

		if message.Event != api.PlayerMessageEventAuth || message.PlayerAuth == nil ||
			!s.CheckPlayerCredential(message.PlayerAuth.Credential) {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventAuthFailed})
			log.Printf("failed to authorize %s", socketID)
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

		var message api.PlayerMessage
		socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
		log.Printf("trying to connect %s", socketID)

		// check authorisation
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s", socketID)
			return
		}

		if message.Event != api.PlayerMessageEventAuth || message.PlayerAuth == nil ||
			!s.CheckPlayerCredential(message.PlayerAuth.Credential) {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventAuthFailed})
			log.Printf("failed to authorize %s", socketID)
			return
		}

		s.listenPlayerPlaySocket(c)
	}))
}

func (s *Server) listenPlayerAdminSocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	log.Printf("authorized %s", socketID)

	var message api.PlayerMessage
	sendPeerStatus := func() {
		answer := api.PlayerMessage{
			Event:              api.PlayerMessageEventPeerStatus,
			PeersStatus:        s.storage.getAll(),
			ParticipantsStatus: s.storage.getParticipantsStatus(),
		}
		if err := c.WriteJSON(answer); err != nil {
			log.Printf("failed to send message %v to %s", answer, socketID)
		}
	}
	sendPeerStatus()
	timer := utils.SetIntervalTimer(PlayerSendPeerStatusInterval, sendPeerStatus)

	if err := c.WriteJSON(api.PlayerMessage{
		Event:    api.PlayerMessageEventInitPeer,
		InitPeer: &api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
	}); err != nil {
		log.Printf("failed to send init_peer%s", socketID)
	}

	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			timer.Stop()
			break
		}

		answer := s.processPlayerMessage(socketID, message)
		if answer == nil {
			continue
		}
		if err := c.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

func (s *Server) listenPlayerPlaySocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	log.Printf("authorized %s", socketID)

	var message api.PlayerMessage
	if err := c.WriteJSON(api.PlayerMessage{
		Event:    api.PlayerMessageEventInitPeer,
		InitPeer: &api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
	}); err != nil {
		log.Printf("failed to send init_peer%s", socketID)
	}

	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			break
		}

		answer := s.processPlayerMessage(socketID, message)
		if answer == nil {
			continue
		}
		if err := c.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

func (s *Server) processPlayerMessage(id sockets.SocketID, m api.PlayerMessage) *api.PlayerMessage {
	playerSocketId := string(id)
	switch m.Event {
	case api.PlayerMessageEventOffer:
		if m.Offer == nil {
			return nil
		}
		playerSocketId := string(id)
		var socket sockets.Socket
		if m.Offer.PeerId != nil {
			socket = s.grabberSockets.GetSocket(sockets.SocketID(*m.Offer.PeerId))
		} else if m.Offer.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Offer.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
		}
		if socket == nil {
			log.Printf("no such grabber with id %v", m.Offer.PeerId)
			return nil
		}
		_ = socket.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventOffer,
			Offer: &api.OfferMessage{
				Offer:      m.Offer.Offer,
				StreamType: m.Offer.StreamType,
				PeerId:     &playerSocketId,
			}})
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}

		var socket sockets.Socket
		if m.Ice.PeerId != nil {
			socket = s.grabberSockets.GetSocket(sockets.SocketID(*m.Ice.PeerId))
		} else if m.Ice.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Ice.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
		}
		if socket == nil {
			log.Printf("no such grabber with id %v to send ice", m.Ice.PeerId)
			return nil
		}
		_ = socket.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventPlayerIce,
			Ice: &api.IceMessage{
				PeerId:    &playerSocketId,
				Candidate: m.Ice.Candidate,
			}})
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

	if err := c.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
			PingInterval:    s.config.GrabberPingInterval,
		},
	}); err != nil {
		log.Printf("failed to send init_peer%s", socketID)
	}

	var message api.GrabberMessage
	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.grabberSockets.CloseSocket(socketID)
			break
		}

		answer := s.processGrabberMessage(socketID, message)
		if answer == nil {
			continue
		}
		if err := c.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

func (s *Server) processGrabberMessage(id sockets.SocketID, m api.GrabberMessage) *api.GrabberMessage {
	grabberId := string(id)
	switch m.Event {
	case api.GrabberMessageEventPing:
		if m.Ping == nil {
			return nil
		}
		s.storage.ping(id, *m.Ping)
	case api.GrabberMessageEventOfferAnswer:
		if m.OfferAnswer == nil {
			return nil
		}
		log.Printf("offer_answer for %s", m.OfferAnswer.PeerId)
		socket := s.playersSockets.GetSocket(sockets.SocketID(m.OfferAnswer.PeerId))
		if socket == nil {
			return nil
		}
		_ = socket.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventOfferAnswer,
			OfferAnswer: &api.OfferAnswerMessage{
				PeerId: grabberId,
				Answer: m.OfferAnswer.Answer,
			},
		})
	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			return nil
		}
		socket := s.playersSockets.GetSocket(sockets.SocketID(*m.Ice.PeerId))
		if socket == nil {
			return nil
		}
		_ = socket.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventGrabberIce,
			Ice: &api.IceMessage{
				PeerId:    &grabberId,
				Candidate: m.Ice.Candidate,
			},
		})
	}
	return nil
}

func LoadServerConfig() (config ServerConfig, err error) {
	configFile, err := os.Open("conf/config.json")
	if err != nil {
		return
	}
	defer func() { _ = configFile.Close() }()
	err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&config)
	if config.ServerPort == 0 {
		config.ServerPort = 8000
	}
	return
}

func parseAdminsNetworks(rawNetworks []string) ([]netip.Prefix, error) {
	result := make([]netip.Prefix, 0, len(rawNetworks))

	for _, r := range rawNetworks {
		network, err := netip.ParsePrefix(r)

		if err != nil {
			return nil, err
		}

		result = append(result, network)
	}

	return result, nil
}
