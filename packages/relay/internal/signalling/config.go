package signalling

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/pion/webrtc/v4"
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
	Codecs               []Codec                  `json:"codecs"`
	WebcamTrackCount     int                      `json:"webcamTrackCount"`
}

type RawCodec struct {
	Params struct {
		MimeType    string `json:"mimeType"`
		ClockRate   uint32 `json:"clockRate"`
		PayloadType uint8  `json:"payloadType"`
		Channels    uint16 `json:"channels"`
	} `json:"params"`
	Type string `json:"type"`
}

type Codec struct {
	Params webrtc.RTPCodecParameters `json:"params"`
	Type   webrtc.RTPCodecType       `json:"type"`
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
	Codecs               []RawCodec               `json:"codecs"`
	WebcamTrackCount     int                      `json:"webcamTrackCount"`
}

func LoadServerConfig() (ServerConfig, error) {
	var rawConfig RawServerConfig

	configFile, err := os.Open("conf/config.json")

	if err != nil {
		return ServerConfig{}, fmt.Errorf("can not open config file, error - %w", err)
	}

	defer func() { _ = configFile.Close() }()

	err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&rawConfig)

	if err != nil {
		return ServerConfig{}, fmt.Errorf("can not decode config file to json - %w", err)
	}

	if rawConfig.ServerPort == 0 {
		rawConfig.ServerPort = 8000
	}

	if rawConfig.WebcamTrackCount == 0 {
		rawConfig.WebcamTrackCount = 2
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
		Codecs:               parseCodecs(rawConfig.Codecs),
		WebcamTrackCount:     rawConfig.WebcamTrackCount,
	}, nil
}

func parseCodecs(rawCodecs []RawCodec) []Codec {
	result := make([]Codec, 0, len(rawCodecs))

	for _, rawCodec := range rawCodecs {
		params := webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:  rawCodec.Params.MimeType,
				ClockRate: rawCodec.Params.ClockRate,
				Channels:  rawCodec.Params.Channels,
			},
			PayloadType: webrtc.PayloadType(rawCodec.Params.PayloadType),
		}

		result = append(result, Codec{Params: params, Type: webrtc.NewRTPCodecType(rawCodec.Type)})
	}

	return result
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
