package config

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strings"

	"github.com/pion/webrtc/v4"
	"gopkg.in/yaml.v3"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
)

type RawServerConfig struct {
	Host                *string `yaml:"host" json:"host"`
	Port                *int    `yaml:"port" json:"port"`
	PublicIP            *string `yaml:"publicIp" json:"publicIp"`
	GrabberPingInterval *int    `yaml:"grabberPingInterval" json:"grabberPingInterval"`
	Title               *string `yaml:"title" json:"title"`
}

func (r RawServerConfig) ToDomain() ServerConfig {
	var cfg ServerConfig
	if r.Host != nil {
		cfg.Host = *r.Host
	}
	if r.Port != nil {
		cfg.Port = *r.Port
	}
	if r.PublicIP != nil {
		cfg.PublicIP = *r.PublicIP
	}
	if r.GrabberPingInterval != nil {
		cfg.GrabberPingInterval = *r.GrabberPingInterval
	}
	if r.Title != nil {
		cfg.Title = *r.Title
	}
	return cfg
}

type RawParticipantInfo struct {
	Name       string `yaml:"name" json:"name"`
	TeamName   string `yaml:"teamName" json:"teamName"`
	University string `yaml:"university" json:"university"`
}

func (r *RawParticipantInfo) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&r.Name)
	}
	type plain RawParticipantInfo
	return node.Decode((*plain)(r))
}

func (r *RawParticipantInfo) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r.Name = s
		return nil
	}
	type plain RawParticipantInfo
	if err := json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	if r.Name == "" {
		return errors.New("participant entry must specify a name")
	}
	return nil
}

type RawSecurityConfig struct {
	PlayerCredential  *string               `yaml:"adminCredential" json:"adminCredential"`
	TLSCrtFile        *string               `yaml:"tlsCrtFile" json:"tlsCrtFile"`
	TLSKeyFile        *string               `yaml:"tlsKeyFile" json:"tlsKeyFile"`
	UploadSecret      *string               `yaml:"uploadSecret" json:"uploadSecret"`
	Participants      *[]RawParticipantInfo `yaml:"participants" json:"participants"`
	AdminsRawNetworks *[]string             `yaml:"adminsNetworks" json:"adminsNetworks"`
}

func (r RawSecurityConfig) ToDomain() (SecurityConfig, error) {
	var cfg SecurityConfig
	cfg.PlayerCredential = r.PlayerCredential
	cfg.TLSCrtFile = r.TLSCrtFile
	cfg.TLSKeyFile = r.TLSKeyFile
	cfg.UploadSecret = r.UploadSecret

	if r.Participants != nil {
		out := make([]ParticipantInfo, 0, len(*r.Participants))
		for _, p := range *r.Participants {
			if p.Name == "" {
				return SecurityConfig{}, errors.New("participant entry must specify a name")
			}
			out = append(out, ParticipantInfo{
				Name:       p.Name,
				TeamName:   p.TeamName,
				University: p.University,
			})
		}
		cfg.Participants = out
	}

	if r.AdminsRawNetworks != nil {
		nets := make([]netip.Prefix, 0, len(*r.AdminsRawNetworks))
		for _, s := range *r.AdminsRawNetworks {
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return SecurityConfig{}, err
			}
			nets = append(nets, p)
		}
		cfg.AdminsRawNetworks = nets
	}

	return cfg, nil
}

type RawWebRTCConfig struct {
	PortMin              *uint16                   `yaml:"portMin" json:"portMin"`
	PortMax              *uint16                   `yaml:"portMax" json:"portMax"`
	PeerConnectionConfig *api.PeerConnectionConfig `yaml:"peerConnectionConfig" json:"peerConnectionConfig"`
	Codecs               *[]RawCodec               `yaml:"codecs" json:"codecs"`
	DisableAudio         *bool                     `yaml:"disableAudio" json:"disableAudio"`
}

type RawCodec struct {
	Params struct {
		MimeType    string `json:"mimeType" yaml:"mimeType"`
		ClockRate   uint32 `json:"clockRate" yaml:"clockRate"`
		PayloadType uint8  `json:"payloadType" yaml:"payloadType"`
		Channels    uint16 `json:"channels" yaml:"channels"`
	} `json:"params" yaml:"params"`
	Type string `json:"type" yaml:"type"`
}

func (r RawWebRTCConfig) ToDomain() WebRTCConfig {
	var cfg WebRTCConfig
	if r.PortMin != nil {
		cfg.PortMin = *r.PortMin
	}
	if r.PortMax != nil {
		cfg.PortMax = *r.PortMax
	}
	if r.PeerConnectionConfig != nil {
		cfg.PeerConnectionConfig = *r.PeerConnectionConfig
	}
	if r.Codecs != nil {
		cfg.Codecs = parseCodecs(*r.Codecs)
	}
	if r.DisableAudio != nil {
		cfg.DisableAudio = *r.DisableAudio
	}
	return cfg
}

type RawRecordConfig struct {
	Timeout    *uint   `yaml:"timeout" json:"timeout"`
	StorageDir *string `yaml:"storageDirectory" json:"storageDirectory"`
}

func (r RawRecordConfig) ToDomain() RecordConfig {
	var cfg RecordConfig
	if r.Timeout != nil {
		cfg.Timeout = *r.Timeout
	}
	if r.StorageDir != nil {
		cfg.StorageDir = *r.StorageDir
		_ = os.MkdirAll(cfg.StorageDir, os.ModePerm)
	}
	return cfg
}

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
