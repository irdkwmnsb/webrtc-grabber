package recorder

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"log"
	"time"
)

type Server struct {
	recorder Recorder
	app      *fiber.App
}

func NewRecorderServer(config Config, app *fiber.App) *Server {
	r := Server{
		app:      app,
		recorder: NewRecorder(config),
	}
	return &r
}

type startRecordingInfo struct {
	Key        string `json:"key"`
	PeerName   string `json:"peerName"`
	StreamType string `json:"streamType"`
	Duration   *int   `json:"duration"`
}

func (r *Server) SetupRouting(rootCnt context.Context) {
	r.app.Post("/record/start", func(ctx *fiber.Ctx) error {
		var data startRecordingInfo
		err := ctx.BodyParser(&data)
		if err != nil {
			return err
		}
		log.Println(data)
		var duration time.Duration
		if data.Duration != nil {
			duration = time.Duration(*data.Duration) * time.Second
		}
		recordId, err := r.recorder.Record(rootCnt, data.Key, data.PeerName, data.StreamType, duration)
		if err != nil {
			return err
		}
		return ctx.SendString(recordId)
	})
}
