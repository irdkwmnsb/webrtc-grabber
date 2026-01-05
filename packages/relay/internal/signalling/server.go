package signalling

import (
	"log/slog"
	"runtime"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/repository/memory"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/service"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sfu"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
)

type Server struct {
	app               *fiber.App
	config            *config.AppConfig
	grabberService    *service.GrabberService
	signallingService *service.SignallingService
	recordingService  *service.RecordingService
	authHandler       *AuthHandler
	playerHandler     *PlayerHandler
	grabberHandler    *GrabberHandler
	oldPeersCleaner   utils.IntervalTimer
	playersSockets    *sockets.SocketPool
	grabberSockets    *sockets.SocketPool
	sfu               sfu.SFU
}

func NewServer(cfg *config.AppConfig, app *fiber.App) (*Server, error) {
	sfu, err := sfu.NewLocalSFU(&cfg.WebRTC, cfg.Server.PublicIP)
	if err != nil {
		return nil, err
	}

	repo := memory.NewGrabberRepository()
	grabberService := service.NewGrabberService(repo, cfg.Security.Participants)
	signallingService := service.NewSignallingService(sfu, repo)

	playersSockets := sockets.NewSocketPool()
	grabberSockets := sockets.NewSocketPool()

	commandSender := NewWebSocketCommandSender(grabberSockets)
	recordingEnabled := cfg.Record.StorageDir != ""
	recordingService := service.NewRecordingService(repo, commandSender, cfg.Record.Timeout, cfg.Record.Timeout, recordingEnabled)

	authHandler := NewAuthHandler(cfg)
	sessionHandler := NewSessionHandler(playersSockets, grabberSockets)
	playerHandler := NewPlayerHandler(grabberService, signallingService, sessionHandler, grabberSockets)
	grabberHandler := NewGrabberHandler(cfg, grabberService, signallingService, sessionHandler)

	server := &Server{
		config:            cfg,
		app:               app,
		playersSockets:    playersSockets,
		grabberSockets:    grabberSockets,
		grabberService:    grabberService,
		signallingService: signallingService,
		recordingService:  recordingService,
		authHandler:       authHandler,
		playerHandler:     playerHandler,
		grabberHandler:    grabberHandler,
		sfu:               sfu,
	}

	server.oldPeersCleaner = utils.SetIntervalTimer(time.Minute, func() {
		_ = server.grabberService.CleanupStale(60 * time.Second)
	})

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			metrics.GoRoutines.Set(float64(runtime.NumGoroutine()))
		}
	}()

	return server, nil
}

func (s *Server) Close() {
	s.oldPeersCleaner.Stop()
	s.playersSockets.Close()
	s.grabberSockets.Close()
	s.sfu.Close()
}

func (s *Server) UpdateConfig(cfg *config.AppConfig) {
	s.config = cfg
	s.grabberService.UpdateParticipants(cfg.Security.Participants)
	s.authHandler.config = cfg
	s.grabberHandler.config = cfg
	slog.Info("server configuration updated")
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

func (s *Server) setupPlayerSockets() {
	s.app.Get("/ws/player/admin", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic in /ws/player/admin", "error", err)
			}
		}()

		if !s.authHandler.AuthenticatePlayer(c) {
			return
		}

		s.playerHandler.HandleAdminSocket(c)
	}))

	s.app.Get("/ws/player/play", websocket.New(func(c *websocket.Conn) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic in /ws/player/play", "error", err)
			}
		}()

		if !s.authHandler.AuthenticatePlayer(c) {
			return
		}

		s.playerHandler.HandlePlaySocket(c)
	}))
}

func (s *Server) setupGrabberSockets() {
	s.app.Get("/ws/peers/:name", websocket.New(func(c *websocket.Conn) {
		peerName := c.Params("name")
		s.grabberHandler.HandleSocket(c, peerName)
	}))
}
