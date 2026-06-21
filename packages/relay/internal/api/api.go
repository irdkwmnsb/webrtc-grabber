package api

import (
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type Peer struct {
	Name                    string                   `json:"name"`
	SocketId                sockets.SocketID         `json:"id"`
	LastPing                *time.Time               `json:"lastPing"`
	ConnectionsCount        int                      `json:"connectionsCount"`
	StreamTypes             []StreamType             `json:"streamTypes"`
	CurrentRecordId         *string                  `json:"currentRecordId"`
	ProctoringActiveStreams []StreamType             `json:"proctoringActiveStreams,omitempty"`
	ProctoringHealth        []ProctoringStreamHealth `json:"proctoringHealth,omitempty"`
	SiteChecks              []SiteCheckResult        `json:"siteChecks,omitempty"`
	TeamName                string                   `json:"teamName,omitempty"`
	University              string                   `json:"university,omitempty"`
}

// SiteCheckResult is one site-reachability probe reported by a grabber. The
// configured sites are expected to be UNreachable, so Reachable == true is the
// alarm condition (the student can open a forbidden site).
type SiteCheckResult struct {
	Url       string `json:"url"`
	Reachable bool   `json:"reachable"`
	CheckedAt int64  `json:"checkedAt,omitempty"` // unix millis
}

type ProctoringStreamHealth struct {
	StreamKey     string `json:"streamKey"`
	LastChunkAt   int64  `json:"lastChunkAt,omitempty"`
	ChunksTotal   uint64 `json:"chunksTotal,omitempty"`
	BytesTotal    uint64 `json:"bytesTotal,omitempty"`
	RecorderState string `json:"recorderState,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	RestartCount  uint32 `json:"restartCount,omitempty"`
	DroppedChunks uint64 `json:"droppedChunks,omitempty"`
	QueueDepth    uint32 `json:"queueDepth,omitempty"`
	Browser       string `json:"browser,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
}

type StreamType string

const (
	DesktopStreamType StreamType = "desktop"
	WebcamStreamType  StreamType = "webcam"
)

type PeerStatus struct {
	ConnectionsCount        int                      `json:"connectionsCount"`
	StreamTypes             []StreamType             `json:"streamTypes"`
	CurrentRecordId         *string                  `json:"currentRecordId"`
	ProctoringActiveStreams []StreamType             `json:"proctoringActiveStreams,omitempty"`
	ProctoringHealth        []ProctoringStreamHealth `json:"proctoringHealth,omitempty"`
	SiteChecks              []SiteCheckResult        `json:"siteChecks,omitempty"`
}

type PlayerAuth struct {
	Credential string `json:"credential"`
}

type IceServer struct {
	Urls       string  `json:"urls" yaml:"urls"`
	Username   *string `json:"username" yaml:"username"`
	Credential *string `json:"credential" yaml:"credential"`
}

type PeerConnectionConfig struct {
	IceServers []IceServer `json:"iceServers" yaml:"iceServers"`
}

func DefaultPeerConnectionConfig() PeerConnectionConfig {
	return PeerConnectionConfig{
		IceServers: []IceServer{},
	}
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

type ProctoringRequest struct {
	Enabled         bool       `json:"enabled"`
	EndsAt          *time.Time `json:"endsAt,omitempty"`
	ChunkDurationMs uint32     `json:"chunkDurationMs"`
	Fps             uint32     `json:"fps"`
	VideoBitrate    uint32     `json:"videoBitrate"`
}
