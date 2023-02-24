package api

import (
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"time"
)

type Peer struct {
	Name             string           `json:"name"`
	SocketId         sockets.SocketID `json:"id"`
	LastPing         *time.Time       `json:"lastPing"`
	ConnectionsCount int              `json:"connectionsCount"`
	StreamTypes      []StreamType     `json:"streamTypes"`
}

type StreamType string

const (
	DesktopStreamType StreamType = "desktop"
	WebcamStreamType  StreamType = "webcam"
)

type PeerStatus struct {
	ConnectionsCount int
	StreamTypes      []StreamType
}

type PlayerAuth struct {
	Credential string `json:"credential"`
}
