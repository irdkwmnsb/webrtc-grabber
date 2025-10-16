package main

import (
	"log"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling"
)

func main() {
	config, err := signalling.LoadServerConfig()
	if err != nil {
		log.Fatal(err)
	}
	app := fiber.New(fiber.Config{
		BodyLimit: 50 * 1024 * 1024,
	})

	server, err := signalling.NewServer(&config, app)

	if err != nil {
		log.Fatalf("can not start signalling server, error - %v", err)
	}

	defer server.Close()

	server.SetupWebSocketsAndApi()
	app.Use(pprof.New())
	app.Static("/", "./asset")
	app.Static("/player", "./asset/player.html")
	app.Static("/capture", "./asset/capture.html")

	if config.ServerTLSCrtFile != nil && config.ServerTLSKeyFile != nil {
		log.Printf("Running TLS http server...")
		log.Fatal(app.ListenTLS(":"+strconv.Itoa(config.ServerPort), *config.ServerTLSCrtFile, *config.ServerTLSKeyFile))
	} else {
		log.Fatal(app.Listen(":" + strconv.Itoa(config.ServerPort)))
	}
}
