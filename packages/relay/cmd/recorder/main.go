package main

import (
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/player_client"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/recorder"
	"log"
)

func main() {
	_, err := recorder.LoadRecorderConfig()
	if err != nil {
		log.Fatal(err)
	}
	//app := fiber.New()
	//server := recorder.NewRecorder(config, app)
	//defer server.Close()

	//server.SetupRouting()
	//app.Static("/", "./asset")
	//app.Static("/player", "./asset/player.html")
	//app.Static("/capture", "./asset/capture.html")

	//log.Fatal(app.Listen(":8000"))
	client := player_client.NewClient(player_client.Config{SignallingUrl: "http://localhost:8000", Credential: "live"})
	err = client.ConnectToPeer("001", "desktop")
	if err != nil {
		log.Println(err)
		return
	}
}
