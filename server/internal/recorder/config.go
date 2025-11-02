package recorder

import (
	"bufio"
	"encoding/json"
	"os"
)

type Config struct {
	MaxRecordDuration    int    `json:"maxRecordDurationSec"`
	SignallingUrl        string `json:"signallingUrl"`
	SignallingCredential string `json:"signallingCredential"`
	RecordingsDirectory  string `json:"recordingsDirectory"`
}

func LoadRecorderConfig() (config Config, err error) {
	configFile, err := os.Open("configs/recorder.json")
	if err != nil {
		return
	}
	defer func() { _ = configFile.Close() }()
	err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&config)
	if config.MaxRecordDuration == 0 {
		config.MaxRecordDuration = 60
	}
	if config.RecordingsDirectory == "" {
		config.RecordingsDirectory = "recordings"
	}
	return
}
