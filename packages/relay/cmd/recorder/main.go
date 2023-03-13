package main

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/recorder"
	"log"
	"os"
	"time"
)

func main() {
	config, err := recorder.LoadRecorderConfig()
	if err != nil {
		log.Fatal(err)
	}

	if err = os.MkdirAll(config.RecordingsDirectory, os.ModePerm); err != nil {
		log.Printf("failed to create output directory %s", err)
	}

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	app := fiber.New()
	server := recorder.NewRecorderServer(config, app)

	server.SetupRouting(ctx)
	app.Static("/recordings", config.RecordingsDirectory, fiber.Static{
		Browse:        true,
		CacheDuration: time.Second,
	})
	app.Static("/record", "./asset/record.html")

	log.Fatal(app.Listen(":8001"))

}
