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
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v3"
)

const PlayerSendPeerStatusInterval = time.Second * 5

type Server struct {
	app             *fiber.App
	config          ServerConfig
	storage         *Storage
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool

	grabberPeerConns     map[sockets.SocketID]map[string]*webrtc.PeerConnection
	playerPeerConns      map[string]map[string]*webrtc.PeerConnection
	grabberTracks        map[sockets.SocketID]map[string][]webrtc.TrackLocal
	subscribers          map[sockets.SocketID]map[string][]*webrtc.PeerConnection
	grabberSetupChannels map[sockets.SocketID]map[string]chan struct{}

	socketToStreamType map[sockets.SocketID]string

	mu sync.Mutex

	api *webrtc.API
}

type PeerConnectionConfig struct {
	webrtc.Configuration
}

func NewServer(config ServerConfig, app *fiber.App) (*Server, error) {

	h264Codec := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
		},
		PayloadType: 96,
	}

	vp8Codec := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeVP8,
			ClockRate: 90000,
		},
		PayloadType: 97,
	}

	vp9Codec := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeVP9,
			ClockRate: 90000,
		},
		PayloadType: 98,
	}

	opusCodec := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		PayloadType: 111,
	}

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(h264Codec, webrtc.RTPCodecTypeVideo); err != nil {
		log.Printf("Failed to register H.264 codec: %v", err)
	}
	if err := mediaEngine.RegisterCodec(vp9Codec, webrtc.RTPCodecTypeVideo); err != nil {
		log.Printf("Failed to register H.264 codec: %v", err)
	}
	if err := mediaEngine.RegisterCodec(vp8Codec, webrtc.RTPCodecTypeVideo); err != nil {
		log.Printf("Failed to register VP8 codec: %v", err)
	}
	if err := mediaEngine.RegisterCodec(opusCodec, webrtc.RTPCodecTypeAudio); err != nil {
		log.Printf("Failed to register Opus codec: %v", err)
	}

	interceptorRegistry := &interceptor.Registry{}

	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		log.Printf("failed register interceptors %v", err)
	}

	internalPliFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		log.Printf("can't create pli factory: %v", err)
	}

	interceptorRegistry.Add(internalPliFactory)

	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptorRegistry))

	server := Server{
		config:               config,
		app:                  app,
		playersSockets:       sockets.NewSocketPool(),
		grabberSockets:       sockets.NewSocketPool(),
		storage:              NewStorage(),
		grabberPeerConns:     make(map[sockets.SocketID]map[string]*webrtc.PeerConnection),
		playerPeerConns:      make(map[string]map[string]*webrtc.PeerConnection),
		grabberTracks:        make(map[sockets.SocketID]map[string][]webrtc.TrackLocal),
		subscribers:          make(map[sockets.SocketID]map[string][]*webrtc.PeerConnection),
		grabberSetupChannels: make(map[sockets.SocketID]map[string]chan struct{}),
		api:                  api,
		socketToStreamType:   make(map[sockets.SocketID]string),
	}
	server.storage.setParticipants(config.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	return &server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()

	for grabberID, pcs := range s.grabberPeerConns {
		for _, pc := range pcs {
			if pc != nil {
				pc.Close()
			}
		}
		delete(s.grabberPeerConns, grabberID)
	}

	for _, pcs := range s.playerPeerConns {
		for _, pc := range pcs {
			if pc != nil {
				pc.Close()
			}
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
	messages := make(chan interface{})
	defer close(messages)

	go func() {
		for msg := range messages {
			if err := c.WriteJSON(msg); err != nil {
				log.Printf("failed to send message to %s: %v", socketID, msg)
				return
			}
		}
	}()

	var message api.PlayerMessage
	sendPeerStatus := func() {
		answer := api.PlayerMessage{
			Event:              api.PlayerMessageEventPeerStatus,
			PeersStatus:        s.storage.getAll(),
			ParticipantsStatus: s.storage.getParticipantsStatus(),
		}
		messages <- answer
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

		answer := s.processPlayerMessage(messages, socketID, message)
		if answer == nil {
			continue
		}
		messages <- answer
	}
}

func (s *Server) listenPlayerPlaySocket(c *websocket.Conn) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	s.playersSockets.AddSocket(c)
	log.Printf("authorized %s", socketID)

	messages := make(chan interface{})
	defer close(messages)

	go func() {
		for msg := range messages {
			if err := c.WriteJSON(msg); err != nil {
				log.Printf("failed to send message to %s: %v", socketID, msg)
				return
			}
		}
	}()

	defer func() {
		s.playersSockets.CloseSocket(socketID)
		delete(s.socketToStreamType, socketID)
		s.mu.Lock()
		if pcs, ok := s.playerPeerConns[string(socketID)]; ok {
			for streamType, pc := range pcs {
				pc.Close()
				for grabberID, subs := range s.subscribers {
					if subsStream, ok := subs[streamType]; ok {
						for i, sub := range subsStream {
							if sub == pc {
								s.subscribers[grabberID][streamType] = append(subsStream[:i], subsStream[i+1:]...)
								if len(s.subscribers[grabberID][streamType]) == 0 {
									if grabberPC, ok := s.grabberPeerConns[grabberID][streamType]; ok {
										grabberPC.Close()
										delete(s.grabberPeerConns[grabberID], streamType)
										if len(s.grabberPeerConns[grabberID]) == 0 {
											delete(s.grabberPeerConns, grabberID)
											delete(s.grabberTracks, grabberID)
											delete(s.subscribers, grabberID)
											delete(s.socketToStreamType, grabberID)
										}
									}
								}
								break
							}
						}
					}
				}
			}
			delete(s.playerPeerConns, string(socketID))
		}
		s.mu.Unlock()
	}()

	var message api.PlayerMessage

	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.playersSockets.CloseSocket(socketID)
			break
		}

		answer := s.processPlayerMessage(messages, socketID, message)
		if answer == nil {
			continue
		}
		messages <- answer
	}
}

// func filterSDPToVP9(offerSDP string) string {
// 	var sd sdp.SessionDescription
// 	if err := sd.Unmarshal([]byte(offerSDP)); err != nil {
// 		log.Printf("Failed to unmarshal SDP: %v", err)
// 		return offerSDP // Return original SDP on error
// 	}

// 	for i, media := range sd.MediaDescriptions {
// 		if media.MediaName.Media == "video" {
// 			var newAttributes []sdp.Attribute
// 			var vp9PayloadType string

// 			for _, attr := range media.Attributes {
// 				if attr.Key == "rtpmap" && strings.Contains(attr.Value, "VP9") {
// 					newAttributes = append(newAttributes, attr)
// 					parts := strings.Split(attr.Value, " ")
// 					if len(parts) > 0 {
// 						vp9PayloadType = parts[0]
// 					}
// 				} else if attr.Key == "fmtp" && vp9PayloadType != "" && strings.HasPrefix(attr.Value, vp9PayloadType+" ") {
// 					newAttributes = append(newAttributes, attr)
// 				} else if attr.Key != "rtpmap" && attr.Key != "fmtp" {
// 					newAttributes = append(newAttributes, attr)
// 				}
// 			}

// 			sd.MediaDescriptions[i].Attributes = newAttributes
// 			if vp9PayloadType != "" {
// 				sd.MediaDescriptions[i].MediaName.Formats = []string{vp9PayloadType}
// 			}
// 		}
// 	}

// 	modifiedSDP, err := sd.Marshal()
// 	if err != nil {
// 		log.Printf("Failed to marshal SDP: %v", err)
// 		return offerSDP
// 	}
// 	return string(modifiedSDP)
// }

func (s *Server) processPlayerMessage(messages chan interface{}, id sockets.SocketID, m api.PlayerMessage) *api.PlayerMessage {
	playerSocketId := string(id)
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
					messages <- api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
					log.Printf("no such stream type %v in grabber with id %v", m.Offer.StreamType, m.Offer.PeerId)
					return nil
				}
				grabberSocketID = peer.SocketId
			}
		} else {
			messages <- api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
			log.Printf("offer missing PeerId or PeerName")
			return nil
		}

		streamType := m.Offer.StreamType

		s.mu.Lock()
		if _, ok := s.grabberPeerConns[grabberSocketID]; !ok {
			s.grabberPeerConns[grabberSocketID] = make(map[string]*webrtc.PeerConnection)
			s.grabberTracks[grabberSocketID] = make(map[string][]webrtc.TrackLocal)
			s.subscribers[grabberSocketID] = make(map[string][]*webrtc.PeerConnection)
			s.grabberSetupChannels[grabberSocketID] = make(map[string]chan struct{})
		}
		s.socketToStreamType[id] = streamType
		if _, ok := s.grabberPeerConns[grabberSocketID][streamType]; !ok {
			log.Printf("NEW CONNECTION %v %v", grabberSocketID, streamType)
			setupChan := make(chan struct{})
			s.grabberSetupChannels[grabberSocketID][streamType] = setupChan
			s.mu.Unlock()

			trackChan := make(chan struct{})
			go s.setupGrabberPeerConnection(grabberSocketID, setupChan, trackChan, streamType)

			<-setupChan
			<-trackChan

			s.mu.Lock()
			if _, ok := s.grabberPeerConns[grabberSocketID][streamType]; !ok {
				s.mu.Unlock()
				messages <- api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
				log.Printf("failed to set up grabber peer connection for %s", grabberSocketID)
				return nil
			}
		}
		// Check tracks before proceeding
		tracks := s.grabberTracks[grabberSocketID][streamType]
		log.Printf("Tracks available for %s/%s: %d", grabberSocketID, streamType, len(tracks))
		if len(tracks) == 0 {
			s.mu.Unlock()
			messages <- api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
			log.Printf("no tracks available for %s/%s", grabberSocketID, streamType)
			return nil
		}
		s.mu.Unlock()

		pcPlayer, err := s.api.NewPeerConnection(s.config.PeerConnectionConfig.WebrtcConfiguration())
		if err != nil {
			log.Printf("failed to create player peer connection %s: %v", playerSocketId, err)
			return nil
		}

		pcPlayer.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
			log.Printf("ICE connection state for player %s stream %s: %s", playerSocketId, streamType, state.String())
			if state == webrtc.ICEConnectionStateDisconnected {
				log.Printf("Player %s stream %s disconnected, waiting for recovery", playerSocketId, streamType)
				log.Printf("Player %s stream %s still disconnected, closing", playerSocketId, streamType)
				pcPlayer.Close()
				s.mu.Lock()
				if playerPcs, ok := s.playerPeerConns[playerSocketId]; ok {
					delete(playerPcs, streamType)
					if len(playerPcs) == 0 {
						delete(s.playerPeerConns, playerSocketId)
					}
				}
				s.mu.Unlock()
			} else if state == webrtc.ICEConnectionStateFailed {
				log.Printf("Player %s stream %s failed, cleaning up", playerSocketId, streamType)
				pcPlayer.Close()
				s.mu.Lock()
				if playerPcs, ok := s.playerPeerConns[playerSocketId]; ok {
					delete(playerPcs, streamType)
					if len(playerPcs) == 0 {
						delete(s.playerPeerConns, playerSocketId)
					}
				}
				s.mu.Unlock()
			}
		})

		s.mu.Lock()
		if _, ok := s.playerPeerConns[playerSocketId]; !ok {
			s.playerPeerConns[playerSocketId] = make(map[string]*webrtc.PeerConnection)
		}
		log.Printf("HELLO123")
		s.playerPeerConns[playerSocketId][streamType] = pcPlayer
		s.subscribers[grabberSocketID][streamType] = append(s.subscribers[grabberSocketID][streamType], pcPlayer)
		log.Printf("Adding %d tracks to player %s", len(tracks), playerSocketId)
		for _, track := range tracks {
			var sender *webrtc.RTPSender
			if sender, err = pcPlayer.AddTrack(track); err != nil {
				log.Printf("failed to add track to player %s: %v", playerSocketId, err)
			}
			go func(sender *webrtc.RTPSender) {
				buf := make([]byte, 1500)
				for {
					if _, _, err := sender.Read(buf); err != nil {
						return
					}
				}
			}(sender)
		}
		s.mu.Unlock()

		// modifiedOfferSDP := filterSDPToVP9(m.Offer.Offer.SDP)
		log.Printf("Player remote SDP offer: %s", m.Offer.Offer.SDP)
		if err := pcPlayer.SetRemoteDescription(m.Offer.Offer); err != nil {
			log.Printf("failed to set remote description for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}

		gsi := string(grabberSocketID)
		pcPlayer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate != nil {
				messages <- api.PlayerMessage{
					Event: api.PlayerMessageEventGrabberIce,
					Ice: &api.IceMessage{
						PeerId:    &gsi,
						Candidate: candidate.ToJSON(),
					},
				}
			}
		})

		answer, err := pcPlayer.CreateAnswer(nil)
		if err != nil {
			log.Printf("failed to create answer for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}
		// log.Printf("Player answer SDP: %s", answer.SDP)
		if err := pcPlayer.SetLocalDescription(answer); err != nil {
			log.Printf("failed to set local description for player %s: %v", playerSocketId, err)
			pcPlayer.Close()
			return nil
		}
		messages <- api.PlayerMessage{
			Event: api.PlayerMessageEventOfferAnswer,
			OfferAnswer: &api.OfferAnswerMessage{
				PeerId: string(grabberSocketID),
				Answer: answer,
			},
		}
	case api.PlayerMessageEventPlayerIce:
		if m.Ice == nil {
			return nil
		}
		log.Printf("PLAYER MESSAGE ICE STREAM TYPE: %v", s.socketToStreamType[id])
		log.Printf("PLAYER LEN: %v", len(s.playerPeerConns[playerSocketId]))
		pcPlayer, ok := s.playerPeerConns[playerSocketId][s.socketToStreamType[id]]
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
	defer func() {
		delete(s.socketToStreamType, socketID)
	}()

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

	var message api.GrabberMessage
	for {
		if err := c.ReadJSON(&message); err != nil {
			log.Printf("disconnected %s caused by %s", socketID, err.Error())
			s.grabberSockets.CloseSocket(socketID)
			delete(s.grabberPeerConns, socketID)
			delete(s.grabberTracks, socketID)
			for _, pcs := range s.subscribers[socketID] {
				for _, pcPlayer := range pcs {
					pcPlayer.Close()
				}
			}
			delete(s.subscribers, socketID)
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
	// log.Printf("GOT GRABBER MESSAGE: %v, Stream Type: %v", m.Event, m.Offer.StreamType)
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
		// log.Printf("offer_answer for %s, SDP: %s", m.OfferAnswer.PeerId, m.OfferAnswer.Answer.SDP)
		s.mu.Lock()
		streamType := s.socketToStreamType[id]
		log.Printf("GrabberMessageEventOfferAnswer STREAM TYPE: %v", s.socketToStreamType[id])
		pc, ok := s.grabberPeerConns[id][streamType]
		if !ok {
			s.mu.Unlock()
			log.Printf("no peer connection for grabber %s", id)
			return nil
		}
		if err := pc.SetRemoteDescription(m.OfferAnswer.Answer); err != nil {
			s.mu.Unlock()
			log.Printf("failed to set remote description for grabber %s: %v", id, err)
			return nil
		}

		if setupChan, ok := s.grabberSetupChannels[id][streamType]; ok {
			close(setupChan)
			delete(s.grabberSetupChannels[id], streamType)
		}
		s.mu.Unlock()
	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			return nil
		}
		log.Printf("GRABBER ICE STREAM TYPE: %v", s.socketToStreamType[id])
		pc, ok := s.grabberPeerConns[id][s.socketToStreamType[id]]
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

func (s *Server) setupGrabberPeerConnection(grabberSocketID sockets.SocketID, setupChan, trackChan chan struct{}, streamType string) {
	log.Printf("Setting up grabber peer connection for %s, streamType=%s", grabberSocketID, streamType)
	grabberConn := s.grabberSockets.GetSocket(grabberSocketID)
	if grabberConn == nil {
		log.Printf("Grabber %s not found", grabberSocketID)
		close(setupChan)
		return
	}

	config := s.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := s.api.NewPeerConnection(config)
	if err != nil {
		log.Printf("failed to create grabber peer connection %s: %v", grabberSocketID, err)
		close(setupChan)
		return
	}

	s.mu.Lock()
	s.socketToStreamType[grabberSocketID] = streamType
	s.grabberPeerConns[grabberSocketID][streamType] = pc
	s.grabberTracks[grabberSocketID][streamType] = []webrtc.TrackLocal{}
	s.subscribers[grabberSocketID][streamType] = []*webrtc.PeerConnection{}
	s.mu.Unlock()

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add video transceiver for grabber %s: %v", grabberSocketID, err)
		pc.Close()
		close(setupChan)
		return
	}

	if streamType == "webcam" {
		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if err != nil {
			log.Printf("failed to add audio transceiver for grabber %s: %v", grabberSocketID, err)
			pc.Close()
			close(setupChan)
			return
		}
	}

	expectedTracks := 1
	if streamType == "webcam" {
		expectedTracks = 2
	}
	tracksReceived := 0

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Track received: ID=%s, Kind=%s, Codec=%s, PayloadType=%d",
			remoteTrack.ID(), remoteTrack.Kind(), remoteTrack.Codec().MimeType, remoteTrack.Codec().PayloadType)
		localTrack, err := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability, remoteTrack.ID(), remoteTrack.StreamID())
		if err != nil {
			log.Printf("failed to create TrackLocal for grabber %s: %v", grabberSocketID, err)
			return
		}

		s.mu.Lock()
		s.grabberTracks[grabberSocketID][streamType] = append(s.grabberTracks[grabberSocketID][streamType], localTrack)
		tracksReceived++
		log.Printf("Tracks received for %s: %d/%d", grabberSocketID, tracksReceived, expectedTracks)
		if tracksReceived >= expectedTracks {
			close(trackChan)
		}
		s.mu.Unlock()

		go func() {
			buffer := make([]byte, 1500)

			for {
				n, _, err := remoteTrack.Read(buffer)
				if err != nil {
					log.Printf("error reading RTP from grabber %s: %v", grabberSocketID, err)
					return
				}
				if _, err := localTrack.Write(buffer[:n]); err != nil {
					log.Printf("error writing RTP to TrackLocal for grabber %s: %v", grabberSocketID, err)
				}
			}
		}()
	})

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			gsi := string(grabberSocketID)
			if err := grabberConn.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventPlayerIce,
				Ice: &api.IceMessage{
					PeerId:    &gsi,
					Candidate: candidate.ToJSON(),
				},
			}); err != nil {
				log.Printf("failed to send ICE candidate to grabber %s: %v", grabberSocketID, err)
			}
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for grabber %s: %s", grabberSocketID, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Grabber %s disconnected or failed, cleaning up", grabberSocketID)
			s.mu.Lock()
			pc.Close()
			delete(s.grabberPeerConns[grabberSocketID], streamType)
			delete(s.grabberTracks[grabberSocketID], streamType)
			for _, pcPlayer := range s.subscribers[grabberSocketID][streamType] {
				pcPlayer.Close()
			}
			delete(s.subscribers[grabberSocketID], streamType)
			if len(s.grabberPeerConns[grabberSocketID]) == 0 {
				delete(s.grabberPeerConns, grabberSocketID)
				delete(s.grabberTracks, grabberSocketID)
				delete(s.subscribers, grabberSocketID)
				delete(s.grabberSetupChannels, grabberSocketID)
			}
			if setupChan, ok := s.grabberSetupChannels[grabberSocketID][streamType]; ok {
				close(setupChan)
				delete(s.grabberSetupChannels[grabberSocketID], streamType)
			}
			s.mu.Unlock()
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("failed to create offer for grabber %s: %v", grabberSocketID, err)
		pc.Close()
		close(setupChan)
		return
	}
	// log.Printf("Grabber offer SDP: %s", offer.SDP)
	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("failed to set local description for grabber %s: %v", grabberSocketID, err)
		pc.Close()
		close(setupChan)
		return
	}

	gsi := string(grabberSocketID)
	if err := grabberConn.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventOffer,
		Offer: &api.OfferMessage{
			Offer:      offer,
			PeerId:     &gsi,
			StreamType: streamType,
		},
	}); err != nil {
		log.Printf("failed to send offer to grabber %s: %v", grabberSocketID, err)
		pc.Close()
		close(setupChan)
		return
	}

	select {
	case <-trackChan:
		log.Printf("All expected tracks received for grabber %s", grabberSocketID)
	case <-timer.C:
		log.Printf("Timeout waiting for tracks from grabber %s", grabberSocketID)
		s.mu.Lock()
		pc.Close()
		delete(s.grabberPeerConns[grabberSocketID], streamType)
		delete(s.grabberTracks[grabberSocketID], streamType)
		delete(s.subscribers[grabberSocketID], streamType)
		s.mu.Unlock()
		close(trackChan)
	}
}
