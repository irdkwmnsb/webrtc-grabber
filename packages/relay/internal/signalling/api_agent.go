package signalling

import (
	"log/slog"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func (s *Server) setupAgentApi() {
	s.app.Route("/api/agent", func(router fiber.Router) {
		router.Post("/:peerName/record_upload", func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}

			peerName := c.Params("peerName")

			file, err := c.FormFile("file")
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("No file to upload")
			}
			if !strings.HasSuffix(file.Filename, ".webm") {
				return c.Status(fiber.StatusBadRequest).SendString("File has incorrect extension")
			}

			slog.Info("store agent record file", "filename", file.Filename, "sizeKB", file.Size/1024)

			destination := path.Join(s.config.Record.StorageDir, peerName+"_"+file.Filename)
			if err := c.SaveFile(file, destination); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Failed to upload file")
			}

			return c.Status(fiber.StatusOK).SendString("OK")
		})
	})
}
