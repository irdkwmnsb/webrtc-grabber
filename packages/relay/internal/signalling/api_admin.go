package signalling

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
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

			timeout := uint(0)
			if req.Timeout != nil {
				timeout = *req.Timeout
			}

			err := s.recordingService.StartRecording(domain.StartRecordingCommand{
				GrabberName: req.PeerName,
				RecordID:    req.RecordId,
				Timeout:     timeout,
			})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to start recording: " + err.Error())
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_stop", func(c *fiber.Ctx) error {
			var req stopRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			err := s.recordingService.StopRecording(domain.StopRecordingCommand{
				GrabberName: req.PeerName,
				RecordID:    req.RecordId,
			})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to stop recording: " + err.Error())
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_upload", func(c *fiber.Ctx) error {
			var req uploadRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			err := s.recordingService.UploadRecording(domain.UploadRecordingCommand{
				GrabberName: req.PeerName,
				RecordID:    req.RecordId,
			})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to upload recording: " + err.Error())
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/players_disconnect/:peerName", func(c *fiber.Ctx) error {
			peerName := c.Params("peerName")

			err := s.recordingService.DisconnectPlayers(domain.DisconnectPlayersCommand{
				GrabberName: peerName,
			})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to disconnect players: " + err.Error())
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})
	})
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
