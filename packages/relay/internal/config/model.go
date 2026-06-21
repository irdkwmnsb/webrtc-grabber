package config

import (
	"net/netip"

	"github.com/pion/webrtc/v4"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
)

type AppConfig struct {
	Server   ServerConfig   `json:"server" yaml:"server"`
	Security SecurityConfig `json:"security" yaml:"security"`
	WebRTC   WebRTCConfig   `json:"webrtc" yaml:"webrtc"`
	Record   RecordConfig   `json:"record" yaml:"record"`
	Debug    DebugConfig    `json:"debug" yaml:"debug"`
}

type DebugConfig struct {
	PprofAddr string `json:"pprofAddr" yaml:"pprofAddr"`
}

type ServerConfig struct {
	Host                string `json:"host" yaml:"host"`
	Port                int    `json:"port" yaml:"port"`
	PublicIP            string `json:"publicIp" yaml:"publicIp"`
	GrabberPingInterval int    `json:"grabberPingInterval" yaml:"grabberPingInterval"`
	Title               string `json:"title" yaml:"title"`
}

type ParticipantInfo struct {
	Name       string `json:"name" yaml:"name"`
	TeamName   string `json:"teamName,omitempty" yaml:"teamName,omitempty"`
	University string `json:"university,omitempty" yaml:"university,omitempty"`
	// Password gates the capture page for this participant when auth is enabled.
	// Never serialized to clients (json:"-") so it can't leak to the dashboard.
	Password string `json:"-" yaml:"password,omitempty"`
}

type SecurityConfig struct {
	PlayerCredential  *string           `json:"adminCredential" yaml:"adminCredential"`
	TLSCrtFile        *string           `json:"tlsCrtFile" yaml:"tlsCrtFile"`
	TLSKeyFile        *string           `json:"tlsKeyFile" yaml:"tlsKeyFile"`
	UploadSecret      *string           `json:"uploadSecret" yaml:"uploadSecret"`
	Participants      []ParticipantInfo `json:"participants" yaml:"participants"`
	AdminsRawNetworks []netip.Prefix    `json:"adminsNetworks" yaml:"adminsNetworks"`
	// AuthEnabled turns on the login page (served at /) that routes admins to the
	// dashboard and students to their capture page. When false, behavior is
	// unchanged: / serves the dashboard and the capture page is open.
	AuthEnabled bool `json:"authEnabled" yaml:"authEnabled"`
	// AdminLogin is the username an admin types on the login page; the password is
	// the existing PlayerCredential (adminCredential). Defaults to "admin".
	AdminLogin *string `json:"adminLogin" yaml:"adminLogin"`
}

type WebRTCConfig struct {
	PortMin              uint16                   `json:"portMin" yaml:"portMin"`
	PortMax              uint16                   `json:"portMax" yaml:"portMax"`
	PeerConnectionConfig api.PeerConnectionConfig `json:"peerConnectionConfig" yaml:"peerConnectionConfig"`
	Codecs               []Codec                  `json:"codecs" yaml:"codecs"`
	DisableAudio         bool                     `json:"disableAudio" yaml:"disableAudio"`
}

type RecordConfig struct {
	Timeout    uint   `json:"timeout" yaml:"timeout"`
	StorageDir string `json:"storageDirectory" yaml:"storageDirectory"`
}

type Codec struct {
	Params webrtc.RTPCodecParameters `json:"params"`
	Type   webrtc.RTPCodecType       `json:"type"`
}

func DefaultAppConfig() AppConfig {
	playerPassword := "live"
	adminLogin := "admin"
	return AppConfig{
		Server: ServerConfig{
			Host:                "0.0.0.0",
			Port:                13478,
			PublicIP:            "",
			GrabberPingInterval: 3000,
			Title:               "webrtc-grabber",
		},
		Security: SecurityConfig{
			PlayerCredential: &playerPassword,
			Participants:     []ParticipantInfo{},
			AdminsRawNetworks: []netip.Prefix{
				netip.MustParsePrefix("0.0.0.0/0"),
			},
			TLSCrtFile:  nil,
			TLSKeyFile:  nil,
			AuthEnabled: false,
			AdminLogin:  &adminLogin,
		},
		WebRTC: WebRTCConfig{
			PortMin:              10000,
			PortMax:              20000,
			PeerConnectionConfig: api.DefaultPeerConnectionConfig(),
			Codecs:               DefaultCodecs(),
			DisableAudio:         false,
		},
		Record: RecordConfig{
			Timeout:    180000,
			StorageDir: "./records",
		},
		Debug: DebugConfig{
			PprofAddr: "127.0.0.1:6060",
		},
	}
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
