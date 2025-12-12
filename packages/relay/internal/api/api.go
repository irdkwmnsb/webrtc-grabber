package api

import (
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type Peer struct {
	Name             string           `json:"name"`
	SocketId         sockets.SocketID `json:"id"`
	LastPing         *time.Time       `json:"lastPing"`
	ConnectionsCount int              `json:"connectionsCount"`
	StreamTypes      []StreamType     `json:"streamTypes"`
	CurrentRecordId  *string          `json:"currentRecordId"`
}

type StreamType string

const (
	DesktopStreamType StreamType = "desktop"
	WebcamStreamType  StreamType = "webcam"
)

type PeerStatus struct {
	ConnectionsCount int          `json:"connectionsCount"`
	StreamTypes      []StreamType `json:"streamTypes"`
	CurrentRecordId  *string      `json:"currentRecordId"`
}

type PlayerAuth struct {
	Credential string `json:"credential"`
}

type PeerConnectionConfig struct {
	IceServers []struct {
		Urls       string  `json:"urls" yaml:"urls"`
		Username   *string `json:"username" yaml:"username"`
		Credential *string `json:"credential" yaml:"credential"`
	} `json:"iceServers" yaml:"iceServers"`
}

func (c PeerConnectionConfig) WebrtcConfiguration() webrtc.Configuration {
	var conf webrtc.Configuration
	for _, server := range c.IceServers {
		var iceServer webrtc.ICEServer
		iceServer.URLs = append(iceServer.URLs, server.Urls)
		if server.Username != nil {
			iceServer.Username = *server.Username
		}
		if server.Credential != nil {
			iceServer.Credential = *server.Credential
		}
		conf.ICEServers = append(conf.ICEServers, iceServer)
	}
	return conf
}
