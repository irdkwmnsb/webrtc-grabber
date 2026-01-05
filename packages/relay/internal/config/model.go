package config

import (
	"net/netip"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/pion/webrtc/v4"
)

type AppConfig struct {
	Server   ServerConfig   `json:"server" yaml:"server"`
	Security SecurityConfig `json:"security" yaml:"security"`
	WebRTC   WebRTCConfig   `json:"webrtc" yaml:"webrtc"`
	Record   RecordConfig   `json:"record" yaml:"record"`
}

type ServerConfig struct {
	Port                int    `json:"port" yaml:"port"`
	PublicIP            string `json:"publicIp" yaml:"publicIp"`
	GrabberPingInterval int    `json:"grabberPingInterval" yaml:"grabberPingInterval"`
}

type SecurityConfig struct {
	PlayerCredential  *string        `json:"adminCredential" yaml:"adminCredential"`
	TLSCrtFile        *string        `json:"tlsCrtFile" yaml:"tlsCrtFile"`
	TLSKeyFile        *string        `json:"tlsKeyFile" yaml:"tlsKeyFile"`
	Participants      []string       `json:"participants" yaml:"participants"`
	AdminsRawNetworks []netip.Prefix `json:"adminsNetworks" yaml:"adminsNetworks"`
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
	return AppConfig{
		Server: ServerConfig{
			Port:                13478,
			PublicIP:            "",
			GrabberPingInterval: 3000,
		},
		Security: SecurityConfig{
			PlayerCredential: &playerPassword,
			Participants:     []string{},
			AdminsRawNetworks: []netip.Prefix{
				netip.MustParsePrefix("0.0.0.0/0"),
			},
			TLSCrtFile: nil,
			TLSKeyFile: nil,
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
