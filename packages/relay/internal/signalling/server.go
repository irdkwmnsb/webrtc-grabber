package signalling

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const PlayerSendPeerStatusInterval = time.Second * 5

type EmulatedClient struct {
	PlayerSocketID  sockets.SocketID
	GrabberSocketID sockets.SocketID
	PeerConn        *webrtc.PeerConnection
	Offer           webrtc.SessionDescription
	Answer          webrtc.SessionDescription
}

type Server struct {
	app             *fiber.App
	config          ServerConfig
	storage         *Storage
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool

	grabberPeerConns map[sockets.SocketID]*webrtc.PeerConnection
	playerPeerConns  map[string]*webrtc.PeerConnection
	grabberTracks    map[sockets.SocketID][]webrtc.TrackLocal
	subscribers      map[sockets.SocketID][]*webrtc.PeerConnection

	mu sync.Mutex
}

func NewServer(config ServerConfig, app *fiber.App) (*Server, error) {
	server := Server{
		config:           config,
		app:              app,
		playersSockets:   sockets.NewSocketPool(),
		grabberSockets:   sockets.NewSocketPool(),
		storage:          NewStorage(),
		grabberPeerConns: make(map[sockets.SocketID]*webrtc.PeerConnection),
		playerPeerConns:  make(map[string]*webrtc.PeerConnection),
		grabberTracks:    make(map[sockets.SocketID][]webrtc.TrackLocal),
		subscribers:      make(map[sockets.SocketID][]*webrtc.PeerConnection),
	}
	server.storage.setParticipants(config.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	return &server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()

	for _, pc := range s.grabberPeerConns {
		if pc != nil {
			pc.Close()
		}
	}

	for _, pc := range s.playerPeerConns {
		if pc != nil {
			pc.Close()
		}
	}
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

	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			timer.Stop()
			break
		}

		answer := s.processPlayerMessage(c, socketID, message)
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

	defer func() {
		s.playersSockets.CloseSocket(socketID)
		if pc, ok := s.playerPeerConns[string(socketID)]; ok {
			pc.Close()
			delete(s.playerPeerConns, string(socketID))
			for grabberID, subs := range s.subscribers {
				for i, sub := range subs {
					if sub == pc {
						s.subscribers[grabberID] = append(subs[:i], subs[i+1:]...)
						break
					}
				}
			}
		}
	}()

	var message api.PlayerMessage

	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			break
		}

		answer := s.processPlayerMessage(c, socketID, message)
		if answer == nil {
			continue
		}
		if err := c.WriteJSON(answer); err != nil {
			log.Printf("failed to send answer %v to %s", answer, socketID)
		}
	}
}

func (s *Server) processPlayerMessage(c *websocket.Conn, id sockets.SocketID, m api.PlayerMessage) *api.PlayerMessage {
	playerSocketId := string(id)
	log.Printf("EVENT: %v", m.Event)
	switch m.Event {
	case api.PlayerMessageEventOffer:
		if m.Offer == nil {
			return nil
		}
		playerSocketId := string(id)
		var grabberSocketID sockets.SocketID
		if m.Offer.PeerId != nil {
			grabberSocketID = sockets.SocketID(*m.Offer.PeerId)
		} else if m.Offer.PeerName != nil {
			if peer, ok := s.storage.getPeerByName(*m.Offer.PeerName); ok {
				if !slices.Contains(peer.StreamTypes, api.StreamType(m.Offer.StreamType)) {
					_ = c.WriteJSON(api.PlayerMessage{
						Event: api.PlayerMessageEventOfferFailed,
						// TODO: add message cause
					})
					log.Printf("no such stream type %v in grabber with id %v",
						m.Offer.StreamType, m.Offer.PeerId)
					return nil
				}
				grabberSocketID = peer.SocketId
			}
		} else {
			_ = c.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventOfferFailed,
			})
			log.Printf("offer missing PeerId or PeerName")
			return nil
		}

		_, ok := s.grabberPeerConns[grabberSocketID]
		if !ok {
			_ = c.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventOfferFailed,
			})
			log.Printf("no grabber peer connection for %s", grabberSocketID)
			return nil
		}

		pcPlayer, err := webrtc.NewPeerConnection(s.config.PeerConnectionConfig.WebrtcConfiguration())
		if err != nil {
			log.Printf("failed to create player peer connection %s: %v", playerSocketId, err)
			return nil
		}

		pcPlayer.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
			log.Printf("ICE connection state for player %s: %s", playerSocketId, state.String())
			if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
				log.Printf("Player %s disconnected or failed, cleaning up", playerSocketId)
				pcPlayer.Close()
				delete(s.playerPeerConns, playerSocketId)
				for grabberID, subs := range s.subscribers {
					for i, sub := range subs {
						if sub == pcPlayer {
							s.subscribers[grabberID] = append(subs[:i], subs[i+1:]...)
							break
						}
					}
				}
			}
		})

		s.playerPeerConns[playerSocketId] = pcPlayer
		s.subscribers[grabberSocketID] = append(s.subscribers[grabberSocketID], pcPlayer)

		if err := pcPlayer.SetRemoteDescription(m.Offer.Offer); err != nil {
			log.Printf("failed to set remote description for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}

		for _, track := range s.grabberTracks[grabberSocketID] {
			if _, err := pcPlayer.AddTrack(track); err != nil {
				log.Printf("failed to add track to player %s: %v", playerSocketId, err)
			}
		}

		gsi := string(grabberSocketID)

		pcPlayer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate != nil {
				if err := c.WriteJSON(api.PlayerMessage{
					Event: api.PlayerMessageEventGrabberIce, // Reusing existing event
					Ice: &api.IceMessage{
						PeerId:    &gsi,
						Candidate: candidate.ToJSON(),
					},
				}); err != nil {
					log.Printf("failed to send ICE candidate to player %s: %v", playerSocketId, err)
				}
			}
		})

		answer, err := pcPlayer.CreateAnswer(nil)
		if err != nil {
			log.Printf("failed to create answer for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}
		if err := pcPlayer.SetLocalDescription(answer); err != nil {
			log.Printf("failed to set local description for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}
		if err := c.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventOfferAnswer,
			OfferAnswer: &api.OfferAnswerMessage{
				PeerId: string(grabberSocketID),
				Answer: answer,
			},
		}); err != nil {
			log.Printf("failed to send answer to player %s: %v", playerSocketId, err)
		}
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}

		pcPlayer, ok := s.playerPeerConns[playerSocketId]
		if !ok {
			log.Printf("no player peer connection for %s", playerSocketId)
			return nil
		}
		if err := pcPlayer.AddICECandidate(m.Ice.Candidate); err != nil {
			log.Printf("failed to add ICE candidate to player %s: %v", playerSocketId, err)
		}
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
	si := string(socketID)

	if err := c.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: s.config.PeerConnectionConfig},
			PingInterval:    s.config.GrabberPingInterval,
		},
	}); err != nil {
		log.Printf("failed to send init_peer for %s: %v", socketID, err)
		return
	}

	config := s.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		log.Printf("failed to create grabber peer connection %s: %v", socketID, err)
		return
	}

	s.grabberPeerConns[socketID] = pc
	s.grabberTracks[socketID] = []webrtc.TrackLocal{}
	s.subscribers[socketID] = []*webrtc.PeerConnection{}

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add video transceiver for grabber %s: %v", socketID, err)
		pc.Close()
		return
	}

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add audio transceiver for grabber %s: %v", socketID, err)
		pc.Close()
		return
	}

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("GOT TRACK: %s (Kind: %s)", remoteTrack.ID(), remoteTrack.Kind())
		localTrack, err := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability, remoteTrack.ID(), remoteTrack.StreamID())
		if err != nil {
			log.Printf("failed to create TrackLocal for grabber %s: %v", socketID, err)
			return
		}
		s.grabberTracks[socketID] = append(s.grabberTracks[socketID], localTrack)

		go func() {
			buffer := make(chan *rtp.Packet, 100)
			go func() {
				for pkt := range buffer {
					for retries := 0; retries < 3; retries++ {
						if err := localTrack.WriteRTP(pkt); err != nil {
							log.Printf("error writing RTP to TrackLocal for grabber %s: %v (retry %d)", socketID, err, retries)
							time.Sleep(100 * time.Millisecond)
							continue
						}
						break
					}
				}
			}()

			for {
				pkt, _, err := remoteTrack.ReadRTP()
				if err != nil {
					log.Printf("error reading RTP from grabber %s: %v", socketID, err)
					return
				}
				if err := localTrack.WriteRTP(pkt); err != nil {
					log.Printf("error writing RTP to TrackLocal for grabber %s: %v (retry %d)", socketID, err, 1)
				}
				// select {
				// case buffer <- pkt:
				// default:
				// 	log.Printf("Buffer full, dropping packet for grabber %s", socketID)
				// }
			}
		}()

		for _, pcPlayer := range s.subscribers[socketID] {
			if _, err := pcPlayer.AddTrack(localTrack); err != nil {
				log.Printf("failed to add track to player from grabber %s: %v", socketID, err)
			}
		}
	})

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		log.Printf("ICE Candidate: %v", candidate)
		if candidate != nil {
			if err := c.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventPlayerIce,
				Ice: &api.IceMessage{
					PeerId:    &si,
					Candidate: candidate.ToJSON(),
				},
			}); err != nil {
				log.Printf("failed to send ICE candidate to grabber %s: %v", socketID, err)
			}
		}
	})

	gatherComplete := make(chan struct{})
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		if state == webrtc.ICEGathererStateComplete {
			close(gatherComplete)
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for grabber %s: %s", socketID, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Grabber %s disconnected or failed, cleaning up", socketID)
			pc.Close()
			delete(s.grabberPeerConns, socketID)
			delete(s.grabberTracks, socketID)
			for _, pcPlayer := range s.subscribers[socketID] {
				pcPlayer.Close()
			}
			delete(s.subscribers, socketID)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("failed to create offer for grabber %s: %v", socketID, err)
		pc.Close()
		return
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("failed to set local description for grabber %s: %v", socketID, err)
		pc.Close()
		return
	}

	select {
	case <-gatherComplete:
		log.Printf("ICE gathering completed for grabber %s", socketID)
	case <-time.After(10 * time.Second):
		log.Printf("ICE gathering timed out for grabber %s", socketID)
		pc.Close()
		return
	}

	log.Printf("Offer SDP for grabber %s: %s", socketID, offer.SDP)

	if err := c.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventOffer,
		Offer: &api.OfferMessage{
			Offer:      offer,
			PeerId:     &si,
			StreamType: "webcam",
		},
	}); err != nil {
		log.Printf("failed to send offer to grabber %s: %v", socketID, err)
		pc.Close()
		return
	}

	var message api.GrabberMessage
	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.grabberSockets.CloseSocket(socketID)
			delete(s.grabberPeerConns, socketID)
			delete(s.grabberTracks, socketID)
			for _, pcPlayer := range s.subscribers[socketID] {
				pcPlayer.Close()
			}
			delete(s.subscribers, socketID)
			pc.Close()
			break
		}

		// Process incoming messages and send responses if necessary
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
	// grabberId := string(id)
	log.Printf("GOT GRABBER MESSAGE: %v", m.Event)
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
		log.Printf("offer_answer for %s, SDP: %s", m.OfferAnswer.PeerId, m.OfferAnswer.Answer.SDP)
		pc, ok := s.grabberPeerConns[id]
		if !ok {
			log.Printf("no peer connection for grabber %s", id)
			return nil
		}
		if err := pc.SetRemoteDescription(m.OfferAnswer.Answer); err != nil {
			log.Printf("failed to set remote description for grabber %s: %v", id, err)
			return nil
		}
	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			return nil
		}
		pc, ok := s.grabberPeerConns[id]
		if !ok {
			log.Printf("no peer connection for grabber %s", id)
			return nil
		}
		if err := pc.AddICECandidate(m.Ice.Candidate); err != nil {
			log.Printf("failed to add ICE candidate from grabber %s: %v", id, err)
		}
	}
	return nil
}
