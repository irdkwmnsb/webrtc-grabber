package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"

	"github.com/irdkwmnsb/webrtc-grabber/pkg/api"
	"github.com/pion/webrtc/v4"
)

// ServerConfig holds the complete server configuration loaded from conf/config.json.
// It includes settings for authentication, network access control, WebRTC parameters,
// and codec configuration.
//
// Configuration is loaded once at startup via LoadServerConfig and passed to
// the server and peer manager components.
type ServerConfig struct {
	// PlayerCredential is an optional password for admin access to player endpoints.
	// If nil, no authentication is required (not recommended for production).
	PlayerCredential *string `json:"adminCredential"`

	// Participants is a list of expected grabber names for monitoring and status display.
	// These names are used in the admin interface to show which grabbers are online/offline.
	Participants []string `json:"participants"`

	// AdminsRawNetworks defines IP address ranges allowed to access admin endpoints.
	// Only clients from these CIDR ranges can connect to /ws/player/admin and /ws/player/play.
	// Example: ["192.168.1.0/24", "10.0.0.0/8"]
	AdminsRawNetworks []netip.Prefix `json:"adminsNetworks"`

	// PeerConnectionConfig contains WebRTC peer connection parameters including
	// ICE servers (STUN/TURN) for NAT traversal.
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig"`

	PublicIP string `json:"publicIp"`

	// GrabberPingInterval specifies how often (in seconds) grabbers should send
	// ping messages to indicate they are still alive.
	GrabberPingInterval int `json:"grabberPingInterval"`

	// ServerPort is the TCP port the WebSocket server listens on.
	// Defaults to 8000 if not specified.
	ServerPort int `json:"serverPort"`

	// ServerTLSCrtFile is the path to the TLS certificate file for HTTPS/WSS.
	// If nil, the server runs without TLS (not recommended for production).
	ServerTLSCrtFile *string `json:"serverTLSCrtFile"`

	// ServerTLSKeyFile is the path to the TLS private key file for HTTPS/WSS.
	// Must be provided together with ServerTLSCrtFile.
	ServerTLSKeyFile *string `json:"serverTLSKeyFile"`

	// Codecs defines the list of audio and video codecs supported by the server.
	// Each codec specifies MIME type, clock rate, payload type, and channel count.
	Codecs []Codec `json:"codecs"`

	// WebcamTrackCount specifies the expected number of tracks for webcam streams.
	// Typically 2 (one video, one audio). Defaults to 2 if not specified.
	// Screen share streams always expect 1 track (video only).
	WebcamTrackCount int    `json:"webcamTrackCount"`
	RecordTimeout    uint   `json:"recordTimeout"`
	RecordStorageDir string `json:"recordStorageDirectory"`
}

// RawCodec represents the JSON structure for codec configuration before conversion
// to WebRTC types. This intermediate representation allows for custom JSON unmarshaling.
type RawCodec struct {
	// Params contains the codec parameters
	Params struct {
		// MimeType identifies the codec (e.g., "video/VP8", "audio/opus")
		MimeType string `json:"mimeType"`

		// ClockRate is the codec's clock rate in Hz (e.g., 90000 for video, 48000 for audio)
		ClockRate uint32 `json:"clockRate"`

		// PayloadType is the RTP payload type identifier (96-127 for dynamic types)
		PayloadType uint8 `json:"payloadType"`

		// Channels specifies the number of audio channels (e.g., 2 for stereo, 0 for video)
		Channels uint16 `json:"channels"`
	} `json:"params"`

	// Type is the codec type as a string ("video" or "audio")
	Type string `json:"type"`
}

// Codec represents a fully parsed codec configuration ready for use with WebRTC API.
type Codec struct {
	// Params contains the WebRTC codec parameters
	Params webrtc.RTPCodecParameters `json:"params"`

	// Type indicates whether this is a video or audio codec
	Type webrtc.RTPCodecType `json:"type"`
}

// RawServerConfig is the intermediate structure used when parsing the JSON configuration file.
// It uses string types for fields that need validation or conversion (like IP networks).
type RawServerConfig struct {
	PlayerCredential     *string                  `json:"adminCredential"`
	Participants         []string                 `json:"participants"`
	AdminsRawNetworks    []string                 `json:"adminsNetworks"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig"`
	PublicIP             string                   `json:"publicIp"`
	GrabberPingInterval  int                      `json:"grabberPingInterval"`
	ServerPort           int                      `json:"serverPort"`
	ServerTLSCrtFile     *string                  `json:"serverTLSCrtFile"`
	ServerTLSKeyFile     *string                  `json:"serverTLSKeyFile"`
	Codecs               []RawCodec               `json:"codecs"`
	WebcamTrackCount     int                      `json:"webcamTrackCount"`
	RecordTimeout        uint                     `json:"recordTimeout"`
	RecordStorageDir     string                   `json:"RecordStorageDirectory"`
}

// LoadServerConfig reads and parses the server configuration from configs/signalling.json.
// It performs validation and applies default values for optional settings.
//
// The function:
//  1. Opens and reads configs/signalling.json
//  2. Parses JSON into RawServerConfig
//  3. Applies defaults: ServerPort (8000), WebcamTrackCount (2)
//  4. Validates and parses AdminsRawNetworks into CIDR prefixes
//  5. Converts RawCodec structures to WebRTC Codec types
//
// Returns an error if:
//   - Configuration file cannot be opened or read
//   - JSON is malformed
//   - IP network CIDR notation is invalid
//
// Example configuration:
//
//	{
//	  "adminCredential": "secretpassword",
//	  "participants": ["office-camera", "conference-room"],
//	  "adminsNetworks": ["192.168.1.0/24", "10.0.0.0/8"],
//	  "peerConnectionConfig": {
//	    "iceServers": [{"urls": ["stun:stun.l.google.com:19302"]}]
//	  },
//	  "grabberPingInterval": 5,
//	  "serverPort": 8443,
//	  "serverTLSCrtFile": "/etc/ssl/cert.pem",
//	  "serverTLSKeyFile": "/etc/ssl/key.pem",
//	  "codecs": [
//	    {
//	      "params": {
//	        "mimeType": "video/VP8",
//	        "clockRate": 90000,
//	        "payloadType": 96,
//	        "channels": 0
//	      },
//	      "type": "video"
//	    }
//	  ],
//	  "webcamTrackCount": 2
//	}
func LoadServerConfig() (ServerConfig, error) {
	var rawConfig RawServerConfig

	configFile, err := os.Open("configs/signalling.json")

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
		return ServerConfig{}, fmt.Errorf("can not parse admins networks, error - %v", err)
	}

	if rawConfig.RecordTimeout <= 0 {
		rawConfig.RecordTimeout = 180000
	}
	if rawConfig.RecordStorageDir != "" {
		err := os.MkdirAll(rawConfig.RecordStorageDir, os.ModePerm)
		if err != nil {
			return ServerConfig{}, fmt.Errorf("can create record directory, error - %v", err)
		}
	}

	return ServerConfig{
		PlayerCredential:     rawConfig.PlayerCredential,
		Participants:         rawConfig.Participants,
		AdminsRawNetworks:    adminsNetworks,
		PeerConnectionConfig: rawConfig.PeerConnectionConfig,
		PublicIP:             rawConfig.PublicIP,
		GrabberPingInterval:  rawConfig.GrabberPingInterval,
		ServerPort:           rawConfig.ServerPort,
		ServerTLSCrtFile:     rawConfig.ServerTLSCrtFile,
		ServerTLSKeyFile:     rawConfig.ServerTLSKeyFile,
		Codecs:               parseCodecs(rawConfig.Codecs),
		WebcamTrackCount:     rawConfig.WebcamTrackCount,
		RecordTimeout:        rawConfig.RecordTimeout,
		RecordStorageDir:     rawConfig.RecordStorageDir,
	}, nil
}

// parseCodecs converts raw codec configurations from JSON into WebRTC codec types.
// It transforms the intermediate RawCodec representation into properly typed
// webrtc.RTPCodecParameters and webrtc.RTPCodecType structures.
//
// The conversion handles:
//   - MIME type string to RTPCodecCapability
//   - Clock rate (Hz) for timing
//   - Channels (for audio codecs)
//   - Payload type for RTP packets
//   - Codec type string ("video"/"audio") to RTPCodecType enum
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

// parseAdminsNetworks converts string CIDR notations into validated netip.Prefix objects.
// This allows efficient IP address range checking for admin access control.
//
// Input strings must be in CIDR notation: "192.168.1.0/24", "10.0.0.0/8", etc.
//
// Returns an error if any network string is not valid CIDR notation.
//
// Example inputs:
//   - "192.168.1.0/24" - local network
//   - "10.0.0.0/8" - private network
//   - "2001:db8::/32" - IPv6 network
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
