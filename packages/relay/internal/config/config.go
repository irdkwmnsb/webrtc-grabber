package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/pion/webrtc/v4"
	"gopkg.in/yaml.v3"
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

	RecordTimeout    uint   `json:"recordTimeout"`
	RecordStorageDir string `json:"recordStorageDirectory"`

	// WebRTCPortMin is the minimum UDP port for WebRTC connections.
	// If 0, the system will use any available port.
	WebRTCPortMin uint16 `json:"webrtcPortMin"`

	// WebRTCPortMax is the maximum UDP port for WebRTC connections.
	// Must be greater than WebRTCPortMin if both are specified.
	WebRTCPortMax uint16 `json:"webrtcPortMax"`

	DisableAudio bool `json:"disableAudio"`
}

func DefaultServerConfig() ServerConfig {
	playerPassword := "live"
	return ServerConfig{
		PlayerCredential: &playerPassword,
		Participants:     []string{},
		AdminsRawNetworks: []netip.Prefix{
			netip.MustParsePrefix("0.0.0.0/0"),
		},
		PeerConnectionConfig: api.DefaultPeerConnectionConfig(),
		PublicIP:             "",
		GrabberPingInterval:  3000,
		ServerPort:           13478,
		ServerTLSCrtFile:     nil,
		ServerTLSKeyFile:     nil,
		Codecs:               DefaultCodecs(),
		RecordTimeout:        180000,
		RecordStorageDir:     "./records",
		WebRTCPortMin:        10000,
		WebRTCPortMax:        20000,
		DisableAudio:         false,
	}
}

type Option func(*ServerConfig)

func WithPlayerCredential(credential *string) Option {
	return func(sc *ServerConfig) {
		sc.PlayerCredential = credential
	}
}

func WithParticipants(participants []string) Option {
	return func(sc *ServerConfig) {
		sc.Participants = participants
	}
}

func WithAdminsRawNetworks(networks []netip.Prefix) Option {
	return func(sc *ServerConfig) {
		sc.AdminsRawNetworks = networks
	}
}

func WithPeerConnectionConfig(config api.PeerConnectionConfig) Option {
	return func(sc *ServerConfig) {
		sc.PeerConnectionConfig = config
	}
}

func WithPublicIP(ip string) Option {
	return func(sc *ServerConfig) {
		sc.PublicIP = ip
	}
}

func WithGrabberPingInterval(interval int) Option {
	return func(sc *ServerConfig) {
		sc.GrabberPingInterval = interval
	}
}

func WithServerPort(port int) Option {
	return func(sc *ServerConfig) {
		sc.ServerPort = port
	}
}

func WithServerTLSCrtFile(crtFile *string) Option {
	return func(sc *ServerConfig) {
		sc.ServerTLSCrtFile = crtFile
	}
}

func WithServerTLSKeyFile(keyFile *string) Option {
	return func(sc *ServerConfig) {
		sc.ServerTLSKeyFile = keyFile
	}
}

func WithTLS(crtFile, keyFile string) Option {
	return func(sc *ServerConfig) {
		sc.ServerTLSCrtFile = &crtFile
		sc.ServerTLSKeyFile = &keyFile
	}
}

func WithCodecs(codecs []Codec) Option {
	return func(sc *ServerConfig) {
		sc.Codecs = codecs
	}
}

func WithRecordTimeout(timeout uint) Option {
	return func(sc *ServerConfig) {
		sc.RecordTimeout = timeout
	}
}

func WithRecordStorageDir(dir string) Option {
	return func(sc *ServerConfig) {
		sc.RecordStorageDir = dir
	}
}

func WithWebRTCPortMin(port uint16) Option {
	return func(sc *ServerConfig) {
		sc.WebRTCPortMin = port
	}
}

func WithWebRTCPortMax(port uint16) Option {
	return func(sc *ServerConfig) {
		sc.WebRTCPortMax = port
	}
}

func WithWebRTCPortRange(min, max uint16) Option {
	return func(sc *ServerConfig) {
		sc.WebRTCPortMin = min
		sc.WebRTCPortMax = max
	}
}

func WithDisableAudio(disable bool) Option {
	return func(sc *ServerConfig) {
		sc.DisableAudio = disable
	}
}

func NewServerConfig(opts ...Option) ServerConfig {
	config := DefaultServerConfig()
	for _, opt := range opts {
		opt(&config)
	}
	return config
}

// RawCodec represents the JSON/YAML structure for codec configuration before conversion
// to WebRTC types. This intermediate representation allows for custom JSON/YAML unmarshaling.
type RawCodec struct {
	// Params contains the codec parameters
	Params struct {
		// MimeType identifies the codec (e.g., "video/VP8", "audio/opus")
		MimeType string `json:"mimeType" yaml:"mimeType"`

		// ClockRate is the codec's clock rate in Hz (e.g., 90000 for video, 48000 for audio)
		ClockRate uint32 `json:"clockRate" yaml:"clockRate"`

		// PayloadType is the RTP payload type identifier (96-127 for dynamic types)
		PayloadType uint8 `json:"payloadType" yaml:"payloadType"`

		// Channels specifies the number of audio channels (e.g., 2 for stereo, 0 for video)
		Channels uint16 `json:"channels" yaml:"channels"`
	} `json:"params" yaml:"params"`

	// Type is the codec type as a string ("video" or "audio")
	Type string `json:"type" yaml:"type"`
}

// Codec represents a fully parsed codec configuration ready for use with WebRTC API.
type Codec struct {
	// Params contains the WebRTC codec parameters
	Params webrtc.RTPCodecParameters `json:"params"`

	// Type indicates whether this is a video or audio codec
	Type webrtc.RTPCodecType `json:"type"`
}

func DefaultCodecs() []Codec {
	return []Codec{
		{
			Params: webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:  "video/VP8",
					ClockRate: 90000,
					Channels:  0,
					RTCPFeedback: []webrtc.RTCPFeedback{
						{Type: "nack"},
						{Type: "nack", Parameter: "pli"},
						{Type: "ccm", Parameter: "fir"},
						{Type: "goog-remb"},
					},
				},
				PayloadType: 96,
			},
			Type: webrtc.RTPCodecTypeVideo,
		},
		{
			Params: webrtc.RTPCodecParameters{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:  "audio/opus",
					ClockRate: 48000,
					Channels:  2,
				},
				PayloadType: 111,
			},
			Type: webrtc.RTPCodecTypeAudio,
		},
	}
}

// RawServerConfig is the intermediate structure used when parsing the JSON/YAML configuration file.
// It uses string types for fields that need validation or conversion (like IP networks).
type RawServerConfig struct {
	PlayerCredential     *string                  `json:"adminCredential" yaml:"adminCredential"`
	Participants         []string                 `json:"participants" yaml:"participants"`
	AdminsRawNetworks    []string                 `json:"adminsNetworks" yaml:"adminsNetworks"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig" yaml:"peerConnectionConfig"`
	PublicIP             string                   `json:"publicIp" yaml:"publicIp"`
	GrabberPingInterval  int                      `json:"grabberPingInterval" yaml:"grabberPingInterval"`
	ServerPort           int                      `json:"serverPort" yaml:"serverPort"`
	ServerTLSCrtFile     *string                  `json:"serverTLSCrtFile" yaml:"serverTLSCrtFile"`
	ServerTLSKeyFile     *string                  `json:"serverTLSKeyFile" yaml:"serverTLSKeyFile"`
	Codecs               []RawCodec               `json:"codecs" yaml:"codecs"`
	RecordTimeout        uint                     `json:"recordTimeout" yaml:"recordTimeout"`
	RecordStorageDir     string                   `json:"RecordStorageDirectory" yaml:"recordStorageDirectory"`
	WebRTCPortMin        uint16                   `json:"webrtcPortMin" yaml:"webrtcPortMin"`
	WebRTCPortMax        uint16                   `json:"webrtcPortMax" yaml:"webrtcPortMax"`
	DisableAudio         bool                     `json:"disableAudio" yaml:"disableAudio"`
}

// LoadServerConfig reads and parses the server configuration from conf/config.yaml or conf/config.json.
// It tries YAML first for backward compatibility, then falls back to JSON if YAML is not found.
// It performs validation and applies default values for optional settings.
//
// The function:
//  1. Tries to open conf/config.yaml, falls back to conf/config.json
//  2. Parses YAML/JSON into RawServerConfig
//  3. Applies defaults: ServerPort (8000)
//  4. Validates and parses AdminsRawNetworks into CIDR prefixes
//  5. Converts RawCodec structures to WebRTC Codec types
//  6. Validates WebRTC port range if specified
//
// Returns an error if:
//   - Configuration file cannot be opened or read
//   - YAML/JSON is malformed
//   - IP network CIDR notation is invalid
//   - WebRTC port range is invalid
//
// Example YAML configuration:
//
//	adminCredential: secretpassword
//	participants:
//	  - office-camera
//	  - conference-room
//	adminsNetworks:
//	  - 192.168.1.0/24
//	  - 10.0.0.0/8
//	peerConnectionConfig:
//	  iceServers:
//	    - urls: stun:stun.l.google.com:19302
//	grabberPingInterval: 5
//	serverPort: 8443
//	webrtcPortMin: 10000
//	webrtcPortMax: 20000
func LoadServerConfig() (ServerConfig, error) {
	var rawConfig RawServerConfig

	configFile, err := os.Open("conf/config.yaml")
	isYAML := true

	if err != nil {
		configFile, err = os.Open("conf/config.json")
		isYAML = false
		if err != nil {
			return DefaultServerConfig(), nil
		}
	}

	defer func() { _ = configFile.Close() }()

	if isYAML {
		err = yaml.NewDecoder(bufio.NewReader(configFile)).Decode(&rawConfig)
		if err != nil {
			return ServerConfig{}, fmt.Errorf("can not decode config file to yaml - %w", err)
		}
	} else {
		err = json.NewDecoder(bufio.NewReader(configFile)).Decode(&rawConfig)
		if err != nil {
			return ServerConfig{}, fmt.Errorf("can not decode config file to json - %w", err)
		}
	}

	opts := buildOptionsFromRawConfig(rawConfig)

	return NewServerConfig(opts...), nil
}

func buildOptionsFromRawConfig(raw RawServerConfig) []Option {
	var opts []Option

	if raw.PlayerCredential != nil {
		opts = append(opts, WithPlayerCredential(raw.PlayerCredential))
	}

	if raw.Participants != nil {
		opts = append(opts, WithParticipants(raw.Participants))
	}

	if raw.AdminsRawNetworks != nil {
		adminsNetworks, err := parseAdminsNetworks(raw.AdminsRawNetworks)
		if err == nil {
			opts = append(opts, WithAdminsRawNetworks(adminsNetworks))
		}
	}

	if raw.PeerConnectionConfig.IceServers != nil {
		opts = append(opts, WithPeerConnectionConfig(raw.PeerConnectionConfig))
	}

	if raw.PublicIP != "" {
		opts = append(opts, WithPublicIP(raw.PublicIP))
	}

	if raw.GrabberPingInterval > 0 {
		opts = append(opts, WithGrabberPingInterval(raw.GrabberPingInterval))
	}

	if raw.ServerPort > 0 {
		opts = append(opts, WithServerPort(raw.ServerPort))
	}

	if raw.ServerTLSCrtFile != nil && raw.ServerTLSKeyFile != nil {
		opts = append(opts, WithTLS(*raw.ServerTLSCrtFile, *raw.ServerTLSKeyFile))
	}

	if raw.Codecs != nil {
		opts = append(opts, WithCodecs(parseCodecs(raw.Codecs)))
	}

	if raw.RecordTimeout > 0 {
		opts = append(opts, WithRecordTimeout(raw.RecordTimeout))
	}

	if raw.RecordStorageDir != "" {
		opts = append(opts, WithRecordStorageDir(raw.RecordStorageDir))
		_ = os.MkdirAll(raw.RecordStorageDir, os.ModePerm)
	}

	if raw.WebRTCPortMin > 0 && raw.WebRTCPortMax > 0 {
		opts = append(opts, WithWebRTCPortRange(raw.WebRTCPortMin, raw.WebRTCPortMax))
	}

	if raw.DisableAudio {
		opts = append(opts, WithDisableAudio(true))
	}

	return opts
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
		capability := webrtc.RTPCodecCapability{
			MimeType:  rawCodec.Params.MimeType,
			ClockRate: rawCodec.Params.ClockRate,
			Channels:  rawCodec.Params.Channels,
		}

		if strings.HasPrefix(strings.ToLower(rawCodec.Params.MimeType), "video/") {
			capability.RTCPFeedback = []webrtc.RTCPFeedback{
				{Type: "nack"},
				{Type: "nack", Parameter: "pli"},
				{Type: "ccm", Parameter: "fir"},
				{Type: "goog-remb"},
			}
		}

		params := webrtc.RTPCodecParameters{
			RTPCodecCapability: capability,
			PayloadType:        webrtc.PayloadType(rawCodec.Params.PayloadType),
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
