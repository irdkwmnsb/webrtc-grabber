package signalling

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/proctoring"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

func (s *Server) setupAdminApi() {
	s.app.Route("/api/admin", func(router fiber.Router) {
		router.Use(basicauth.New(basicauth.Config{
			Realm: "Forbidden",
			Authorizer: func(user, pass string) bool {
				return s.config.Security.PlayerCredential == nil || user == "admin" && pass == *s.config.Security.PlayerCredential
			},
		}))

		router.Post("/record_start", func(c *fiber.Ctx) error {
			var req startRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			recordTimeout := s.config.Record.Timeout
			if req.Timeout != nil {
				recordTimeout = min(*req.Timeout, recordTimeout)
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventRecordStart,
				RecordStart: &api.RecordStartMessage{
					RecordId:    req.RecordId,
					TimeoutMsec: recordTimeout,
				}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send start recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_stop", func(c *fiber.Ctx) error {
			var req stopRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event:      api.GrabberMessageEventRecordStop,
				RecordStop: &api.RecordStopMessage{RecordId: req.RecordId}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send stop recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_upload", func(c *fiber.Ctx) error {
			var req uploadRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event:        api.GrabberMessageEventRecordUpload,
				RecordUpload: &api.RecordUploadMessage{RecordId: req.RecordId}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send stop recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/players_disconnect/:peerName", func(c *fiber.Ctx) error {
			peerName := c.Params("peerName")

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(peerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{Event: api.GrabberMessageEventPlayersDisconnect})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send disconnect players request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/proctoring/start", func(c *fiber.Ctx) error {
			var req api.ProctoringRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			err := s.proctoring.Start(proctoring.StartConfig{
				EndsAt:          req.EndsAt,
				ChunkDurationMs: req.ChunkDurationMs,
				Fps:             req.Fps,
				VideoBitrate:    req.VideoBitrate,
			})

			if errors.Is(err, proctoring.ErrAlreadyActive) {
				return c.Status(fiber.StatusConflict).SendString(err.Error())
			}

			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
			}

			return c.JSON(s.proctoring.Get())
		})

		router.Post("/proctoring/pause", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Pause, s.proctoring.Get)
		})

		router.Post("/proctoring/resume", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Resume, s.proctoring.Get)
		})

		router.Post("/proctoring/stop", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Stop, s.proctoring.Get)
		})

		router.Post("/proctoring/finalize/:sessionId", func(c *fiber.Ctx) error {
			sessionId := c.Params("sessionId")
			if !isSafePathSegment(sessionId) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid sessionId")
			}
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}
			finalizeProctoringSession(s.config.Record.StorageDir, sessionId)
			return c.Status(fiber.StatusOK).SendString("OK")
		})

	})
}

func proctoringAction(c *fiber.Ctx, action func() error, getter func() proctoring.State) error {
	err := action()
	switch {
	case errors.Is(err, proctoring.ErrNoSession),
		errors.Is(err, proctoring.ErrNotPaused),
		errors.Is(err, proctoring.ErrNotActive):
		return c.Status(fiber.StatusConflict).SendString(err.Error())
	case err != nil:
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	return c.JSON(getter())
}

type startRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
	Timeout  *uint  `json:"timeout"`
}

type stopRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
}

type uploadRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
}
