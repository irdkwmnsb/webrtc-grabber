package signalling

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/asset"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/proctoring"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sfu"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
)

const (
	PlayerSendPeerStatusInterval = time.Second * 5
)

type Server struct {
	ctx             context.Context
	cancel          context.CancelFunc
	app             *fiber.App
	config          *config.AppConfig
	storage         *Storage
	oldPeersCleaner utils.IntervalTimer
	playersSockets  *sockets.SocketPool
	grabberSockets  *sockets.SocketPool
	sfu             sfu.SFU
	proctoring      *proctoring.Manager
	uploadSecret    []byte
}

func NewServer(parent context.Context, cfg *config.AppConfig, app *fiber.App) (*Server, error) {

	ctx, cancel := context.WithCancel(parent)

	sfu, err := sfu.NewLocalSFU(ctx, &cfg.WebRTC, cfg.Server.PublicIP, 0)
	if err != nil {
		cancel()
		return nil, err
	}

	uploadSecret, configured, err := newUploadSecret(cfg.Security.UploadSecret)
	if err != nil {
		cancel()
		return nil, err
	}
	if !configured {
		slog.Warn("security.uploadSecret not configured; generated ephemeral secret (won't survive restart)")
	}

	server := &Server{
		ctx:            ctx,
		cancel:         cancel,
		config:         cfg,
		app:            app,
		playersSockets: sockets.NewSocketPool(),
		grabberSockets: sockets.NewSocketPool(),
		storage:        NewStorage(),
		sfu:            sfu,
		uploadSecret:   uploadSecret,
	}

	manager, err := proctoring.NewManager(cfg.Record.StorageDir, server.broadcastProctoring)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("init proctoring manager: %w", err)
	}
	server.proctoring = manager

	sweepProctoringSessions(cfg.Record.StorageDir, manager.Get().SessionId)

	server.storage.setParticipants(cfg.Security.Participants)
	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, server.storage.deleteOldPeers)

	metrics.StartTime.Set(float64(time.Now().Unix()))

	for i := range sfu.NumEventShards() {
		go server.runEventConsumer(sfu.EventShard(i))
	}

	return server, nil
}

func (s *Server) runEventConsumer(events <-chan sfu.Event) {
	for event := range events {
		s.dispatchEvent(event)
	}
}

func (s *Server) dispatchEvent(event sfu.Event) {
	switch event.Kind {
	case sfu.EventPublisherOffer:
		sock := s.grabberSockets.GetSocket(event.PublisherID)
		if sock == nil {
			return
		}
		key := sfu.PublisherKey(event.PublisherID, event.StreamType)
		_ = sock.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventOffer,
			Offer: &api.OfferMessage{
				Offer:      *event.SDP,
				PeerId:     &key,
				StreamType: event.StreamType,
			},
		})
	case sfu.EventPublisherICECandidate:
		sock := s.grabberSockets.GetSocket(event.PublisherID)
		if sock == nil {
			return
		}
		key := sfu.PublisherKey(event.PublisherID, event.StreamType)
		_ = sock.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventPlayerIce,
			Ice:   &api.IceMessage{PeerId: &key, Candidate: *event.ICE},
		})
	case sfu.EventSubscriberICECandidate:
		sock := s.playersSockets.GetSocket(event.SubscriberID)
		if sock == nil {
			return
		}
		key := sfu.PublisherKey(event.PublisherID, event.StreamType)
		_ = sock.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventGrabberIce,
			Ice:   &api.IceMessage{PeerId: &key, Candidate: *event.ICE},
		})
	}
}

func (s *Server) Close() {
	s.cancel()
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
	s.sfu.Close()
	if s.proctoring != nil {
		s.proctoring.Close()
	}
}

func (s *Server) UpdateConfig(cfg *config.AppConfig) {
	s.config = cfg // not critical (race condition)
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
	s.setupPublicApi()
}

func (s *Server) setupPublicApi() {
	s.app.Get("/api/ui-config", func(c *fiber.Ctx) error {
		title := s.config.Server.Title
		if title == "" {
			title = "webrtc-grabber"
		}
		return c.JSON(fiber.Map{
			"title":       title,
			"authEnabled": s.authEnabled(),
			"siteCheck":   s.siteCheckClientConfig(),
		})
	})

	s.app.Post("/api/auth/login", s.handleLogin)
	s.app.Post("/api/auth/logout", s.handleLogout)
	s.app.Get("/api/auth/logout", s.handleLogout)

	s.setupPageRoutes()
}

// siteCheckClientConfig returns the site-probe config handed to capture pages,
// or nil when no sites are configured (feature off). The interval is clamped to
// a sane floor so a misconfiguration can't hammer the network.
func (s *Server) siteCheckClientConfig() *api.SiteCheckClientConfig {
	sites := s.config.SiteCheck.Sites
	if len(sites) == 0 {
		return nil
	}
	interval := s.config.SiteCheck.IntervalMs
	if interval < 1000 {
		interval = 15000
	}
	return &api.SiteCheckClientConfig{Sites: sites, IntervalMs: interval}
}

type siteCheckPeerStatus struct {
	Name       string                `json:"name"`
	TeamName   string                `json:"teamName,omitempty"`
	Online     bool                  `json:"online"`
	SiteChecks []api.SiteCheckResult `json:"siteChecks"`
}

type siteCheckStatusResponse struct {
	Sites      []string              `json:"sites"`
	IntervalMs int                   `json:"intervalMs"`
	Peers      []siteCheckPeerStatus `json:"peers"`
}

// buildSiteCheckStatus reports the configured sites plus each participant's most
// recent probe results, for the admin dashboard / proctoring page.
func (s *Server) buildSiteCheckStatus() siteCheckStatusResponse {
	resp := siteCheckStatusResponse{
		Sites:      s.config.SiteCheck.Sites,
		IntervalMs: s.config.SiteCheck.IntervalMs,
		Peers:      []siteCheckPeerStatus{},
	}
	if resp.Sites == nil {
		resp.Sites = []string{}
	}
	for _, p := range s.storage.getParticipantsStatus() {
		checks := p.SiteChecks
		if checks == nil {
			checks = []api.SiteCheckResult{}
		}
		resp.Peers = append(resp.Peers, siteCheckPeerStatus{
			Name:       p.Name,
			TeamName:   p.TeamName,
			Online:     p.LastPing != nil,
			SiteChecks: checks,
		})
	}
	return resp
}

// sendAsset writes an embedded asset (see package asset) with the right
// Content-Type inferred from its extension.
func sendAsset(c *fiber.Ctx, name string) error {
	data, err := asset.FS.ReadFile(name)
	if err != nil {
		return fiber.ErrNotFound
	}
	c.Type(filepath.Ext(name))
	return c.Send(data)
}

// setupPageRoutes registers the HTML entrypoints. They run before main.go's
// static handler, so they take precedence for these exact paths, and main.go
// blocks direct access to the raw .html files — so these guarded routes are the
// only way to reach a page. Pages are served from the embedded asset FS. When
// auth is disabled, "/" keeps serving the dashboard (unchanged behavior). The
// hard access boundary remains the WebSocket token check in authorizeGrabber;
// these page guards are the matching first line of defense.
func (s *Server) setupPageRoutes() {
	s.app.Get("/", func(c *fiber.Ctx) error {
		if s.authEnabled() {
			return sendAsset(c, "login.html")
		}
		return sendAsset(c, "index.html")
	})

	s.app.Get("/login", func(c *fiber.Ctx) error {
		return sendAsset(c, "login.html")
	})

	s.app.Get("/admin", func(c *fiber.Ctx) error {
		return sendAsset(c, "index.html")
	})

	s.app.Get("/proctoring", func(c *fiber.Ctx) error {
		return sendAsset(c, "proctoring.html")
	})

	s.app.Get("/player", func(c *fiber.Ctx) error {
		return sendAsset(c, "player.html")
	})

	s.app.Get("/capture", func(c *fiber.Ctx) error {
		if s.authEnabled() {
			name, ok := s.verifyCaptureToken(c.Cookies(captureCookieName))
			if !ok {
				return c.Redirect("/login")
			}
			// The cookie, not the URL, decides which peer this student is.
			if c.Query("peerName") != name {
				return c.Redirect("/capture?peerName=" + url.QueryEscape(name))
			}
		}
		return sendAsset(c, "capture.html")
	})
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
	recoverHelper := func(route string) func() {
		return func() {
			if err := recover(); err != nil {
				slog.Error(fmt.Sprintf("panic in %s", route), "error", err)
				return
			}
		}
	}

	s.app.Get("/ws/player/admin", websocket.New(func(c *websocket.Conn) {
		defer recoverHelper("/ws/player/admin")()

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
		defer recoverHelper("/ws/player/play")()

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

	state := s.proctoring.Get()
	if state.SessionId != "" {
		_ = c.WriteJSON(api.PlayerMessage{
			Event:      api.PlayerMessageEventProctoring,
			Proctoring: &state,
		})
	}

	return true
}

func (s *Server) listenPlayerSocket(c *websocket.Conn, createCallback func(socket sockets.Socket) func()) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	slog.Info("authorized", "socketID", socketID)

	newC := s.playersSockets.AddSocket(c)
	callback := createCallback(newC)
	callback()
	timer := utils.SetIntervalTimer(PlayerSendPeerStatusInterval, callback)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-s.ctx.Done():
			_ = newC.Close()
		case <-done:
		}
	}()

	metrics.ActivePlayers.Inc()
	metrics.PlayersConnectedTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()
	defer func() {
		s.playersSockets.CloseSocket(socketID)
		timer.Stop()
		s.sfu.Unsubscribe(socketID)
		metrics.ActivePlayers.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
	}()

	var message api.PlayerMessage
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

		var grabberSocketID sockets.SocketID
		if m.Offer.PeerId != nil {
			grabberSocketID = sockets.SocketID(*m.Offer.PeerId)
		} else if m.Offer.PeerName != nil {
			peer, ok := s.storage.getPeerByName(*m.Offer.PeerName)
			if !ok {
				_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
				return nil
			}
			if !slices.Contains(peer.StreamTypes, api.StreamType(m.Offer.StreamType)) {
				_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
				slog.Warn("no such stream type in grabber", "streamType", m.Offer.StreamType, "peerName", *m.Offer.PeerName)
				return nil
			}
			grabberSocketID = peer.SocketId
		} else {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			return nil
		}

		if s.grabberSockets.GetSocket(grabberSocketID) == nil {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			return nil
		}

		answer, err := s.sfu.Subscribe(id, grabberSocketID, m.Offer.StreamType, m.Offer.Offer)
		if err != nil {
			_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
			return nil
		}
		publisherKey := sfu.PublisherKey(grabberSocketID, m.Offer.StreamType)
		_ = c.WriteJSON(api.PlayerMessage{
			Event:       api.PlayerMessageEventOfferAnswer,
			OfferAnswer: &api.OfferAnswerMessage{PeerId: publisherKey, Answer: answer},
		})
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

		if !s.authorizeGrabber(c, peerName) {
			return
		}

		s.storage.addPeer(peerName, socketID)

		s.listenGrabberSocket(c, peerName)
	}))
}

// authorizeGrabber enforces the capture token when auth is enabled: the
// connection must present a valid cookie issued for exactly this peerName.
// When auth is disabled it always allows (unchanged behavior).
func (s *Server) authorizeGrabber(c *websocket.Conn, peerName string) bool {
	if !s.authEnabled() {
		return true
	}
	name, ok := s.verifyCaptureToken(c.Cookies(captureCookieName))
	if !ok || name != peerName {
		slog.Warn("rejecting unauthorized grabber", "peerName", peerName)
		_ = c.WriteJSON(fiber.Map{
			"event":   "auth_failed",
			"message": "unauthorized: please log in again",
		})
		return false
	}
	return true
}

func (s *Server) listenGrabberSocket(c *websocket.Conn, peerName string) {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())
	newC := s.grabberSockets.AddSocket(c)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-s.ctx.Done():
			_ = newC.Close()
		case <-done:
		}
	}()

	metrics.ActiveAgents.Inc()
	metrics.AgentsRegisteredTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()
	defer func() {
		s.grabberSockets.CloseSocket(socketID)
		s.sfu.DropPublisher(socketID)
		s.storage.removePeer(socketID)
		metrics.ActiveAgents.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
	}()

	if err := newC.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: s.config.WebRTC.PeerConnectionConfig},
			PingInterval:    s.config.Server.GrabberPingInterval,
			SiteCheck:       s.siteCheckClientConfig(),
		},
	}); err != nil {
		slog.Error("failed to send init_peer", "socketID", socketID, "error", err)
		return
	}

	if state := s.proctoring.Get(); state.IsActive() || state.IsPaused() {
		cfg := &api.ProctoringConfigMessage{
			SessionId:       state.SessionId,
			ChunkDurationMs: state.ChunkDurationMs,
			Fps:             state.Fps,
			VideoBitrate:    state.VideoBitrate,
			UploadTokens:    s.proctoringUploadTokens(state.SessionId, peerName),
		}
		var msg api.GrabberMessage
		if state.IsActive() {
			msg = api.GrabberMessage{
				Event:           api.GrabberMessageEventProctoringStart,
				ProctoringStart: cfg,
			}
		} else {
			_ = newC.WriteJSON(api.GrabberMessage{
				Event:           api.GrabberMessageEventProctoringStart,
				ProctoringStart: cfg,
			})
			msg = api.GrabberMessage{
				Event: api.GrabberMessageEventProctoringPause,
			}
		}
		if err := newC.WriteJSON(msg); err != nil {
			slog.Error("failed to send proctoring catch-up", "socketID", socketID, "error", err)
		}
	}

	var message api.GrabberMessage
	for {
		if err := newC.ReadJSON(&message); err != nil {
			slog.Debug("grabber disconnected", "socketID", socketID, "error", err.Error())
			return
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
		s.storage.ping(id, m.Ping)
	case api.GrabberMessageEventOfferAnswer:
		if m.OfferAnswer == nil || m.OfferAnswer.PeerId == "" {
			return nil
		}
		s.sfu.PublisherAnswer(m.OfferAnswer.PeerId, m.OfferAnswer.Answer)

	case api.GrabberMessageEventGrabberIce:
		if m.Ice == nil || m.Ice.PeerId == nil {
			return nil
		}
		s.sfu.PublisherICE(*m.Ice.PeerId, m.Ice.Candidate)
	}
	return nil
}

func (s *Server) broadcastProctoring(prev, next proctoring.State) {
	s.broadcastToPlayers(api.PlayerMessage{
		Event:      api.PlayerMessageEventProctoring,
		Proctoring: &next,
	})

	buildCfg := func(peerName string) *api.ProctoringConfigMessage {
		return &api.ProctoringConfigMessage{
			SessionId:       next.SessionId,
			ChunkDurationMs: next.ChunkDurationMs,
			Fps:             next.Fps,
			VideoBitrate:    next.VideoBitrate,
			UploadTokens:    s.proctoringUploadTokens(next.SessionId, peerName),
		}
	}

	switch {
	case prev.IsIdle() && next.IsActive():
		s.preCreateProctoringDirs(next.SessionId)
		s.broadcastToGrabbersPerPeer(func(peerName string) api.GrabberMessage {
			return api.GrabberMessage{
				Event:           api.GrabberMessageEventProctoringStart,
				ProctoringStart: buildCfg(peerName),
			}
		})
	case prev.IsActive() && next.IsPaused():
		s.broadcastToGrabbers(api.GrabberMessage{
			Event: api.GrabberMessageEventProctoringPause,
		})
	case prev.IsPaused() && next.IsActive():
		s.broadcastToGrabbersPerPeer(func(peerName string) api.GrabberMessage {
			return api.GrabberMessage{
				Event:            api.GrabberMessageEventProctoringResume,
				ProctoringResume: buildCfg(peerName),
			}
		})
	case !prev.IsIdle() && next.IsIdle():
		s.broadcastToGrabbers(api.GrabberMessage{
			Event: api.GrabberMessageEventProctoringStop,
		})
		s.scheduleProctoringFinalize(prev.SessionId)
	}
}

func (s *Server) preCreateProctoringDirs(sessionId string) {
	if sessionId == "" || s.config.Record.StorageDir == "" {
		return
	}
	for _, name := range s.storage.peerNamesBySocketId() {
		for _, sk := range proctoringStreamKeys() {
			dir := filepath.Join(s.config.Record.StorageDir, "proctoring", sessionId, name, sk)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				slog.Warn("failed to pre-create proctoring dir", "dir", dir, "error", err)
			}
		}
	}
}

func (s *Server) broadcastToGrabbersPerPeer(buildMsg func(peerName string) api.GrabberMessage) {
	for socketID, peerName := range s.storage.peerNamesBySocketId() {
		sock := s.grabberSockets.GetSocket(socketID)
		if sock == nil {
			continue
		}
		msg := buildMsg(peerName)
		if err := sock.WriteJSON(msg); err != nil {
			slog.Debug("broadcast to grabber failed", "event", msg.Event, "error", err)
		}
	}
}

func (s *Server) broadcastToPlayers(msg api.PlayerMessage) {
	for _, sock := range s.playersSockets.Snapshot() {
		if err := sock.WriteJSON(msg); err != nil {
			slog.Debug("broadcast to player failed", "event", msg.Event, "error", err)
		}
	}
}

func (s *Server) broadcastToGrabbers(msg api.GrabberMessage) {
	for _, sock := range s.grabberSockets.Snapshot() {
		if err := sock.WriteJSON(msg); err != nil {
			slog.Debug("broadcast to grabber failed", "event", msg.Event, "error", err)
		}
	}
}
