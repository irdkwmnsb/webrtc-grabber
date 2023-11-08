package main

import (
	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling"
	"log"
	"strconv"
)

func main() {
	config, err := signalling.LoadServerConfig()
	if err != nil {
		log.Fatal(err)
	}
	app := fiber.New()
	server := signalling.NewServer(config, app)
	defer server.Close()

	server.SetupWebSockets()
	app.Static("/", "./asset")
	app.Static("/player", "./asset/player.html")
	app.Static("/capture", "./asset/capture.html")

	log.Fatal(app.Listen(":" + strconv.Itoa(config.ServerPort)))
}
