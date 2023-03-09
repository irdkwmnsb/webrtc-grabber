package recorder

import (
	"bufio"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"os"
)

type Config struct {
	GrabberCredential *string `json:"grabberCredential"`
	MaxRecordDuration int     `json:"maxRecordDurationSec"`
}

type Recorder struct {
	config Config
	app    *fiber.App
}

func NewRecorder(config Config, app *fiber.App) *Recorder {
	r := Recorder{
		config: config,
		app:    app,
	}
	if r.config.MaxRecordDuration == 0 {
		r.config.MaxRecordDuration = 120
	}
	return &r
}

func (r *Recorder) SetupRouting() {
	r.app.Post("/record/start", func(ctx *fiber.Ctx) error {
		//peerName := ctx.Get("peerName")
		return nil
	})
}

func LoadRecorderConfig() (config Config, err error) {
	configFile, err := os.Open("conf/recorder.json")
	if err != nil {
		return
	}
	defer func() { _ = configFile.Close() }()
	err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&config)
	return
}
