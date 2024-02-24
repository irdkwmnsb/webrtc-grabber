package signalling

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"net/netip"
	"os"
)

type ServerConfig struct {
	PlayerCredential     *string                  `json:"adminCredential"`
	Participants         []string                 `json:"participants"`
	AdminsRawNetworks    []netip.Prefix           `json:"adminsNetworks"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig"`
	GrabberPingInterval  int                      `json:"grabberPingInterval"`
	ServerPort           int                      `json:"serverPort"`
	ServerTLSCrtFile     *string                  `json:"serverTLSCrtFile"`
	ServerTLSKeyFile     *string                  `json:"serverTLSKeyFile"`
}

type RawServerConfig struct {
	PlayerCredential     *string                  `json:"adminCredential"`
	Participants         []string                 `json:"participants"`
	AdminsRawNetworks    []string                 `json:"adminsNetworks"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig"`
	GrabberPingInterval  int                      `json:"grabberPingInterval"`
	ServerPort           int                      `json:"serverPort"`
	ServerTLSCrtFile     *string                  `json:"serverTLSCrtFile"`
	ServerTLSKeyFile     *string                  `json:"serverTLSKeyFile"`
}

func LoadServerConfig() (ServerConfig, error) {
	var rawConfig RawServerConfig

	configFile, err := os.Open("conf/config.json")

	if err != nil {
		return ServerConfig{}, fmt.Errorf("can not open config file, error - %w", err)
	}

	defer func() { _ = configFile.Close() }()

	err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&rawConfig)

	if rawConfig.ServerPort == 0 {
		rawConfig.ServerPort = 8000
	}

	adminsNetworks, err := parseAdminsNetworks(rawConfig.AdminsRawNetworks)

	if err != nil {
		return ServerConfig{}, fmt.Errorf("can not parse admins networks, error - %w", err)
	}

	return ServerConfig{
		PlayerCredential:     rawConfig.PlayerCredential,
		Participants:         rawConfig.Participants,
		AdminsRawNetworks:    adminsNetworks,
		PeerConnectionConfig: rawConfig.PeerConnectionConfig,
		GrabberPingInterval:  rawConfig.GrabberPingInterval,
		ServerPort:           rawConfig.ServerPort,
		ServerTLSCrtFile:     rawConfig.ServerTLSCrtFile,
		ServerTLSKeyFile:     rawConfig.ServerTLSKeyFile,
	}, nil
}

func parseAdminsNetworks(rawNetworks []string) ([]netip.Prefix, error) {
	result := make([]netip.Prefix, 0, len(rawNetworks))

	for _, r := range rawNetworks {
		network, err := netip.ParsePrefix(r)

		if err != nil {
			return nil, err
		}

		result = append(result, network)
	}

	return result, nil
}
