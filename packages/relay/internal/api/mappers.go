package api

import (
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

func ToApiPeer(g domain.Grabber) Peer {
	var lastPing *time.Time
	if !g.LastPing.IsZero() {
		t := g.LastPing
		lastPing = &t
	}

	streamTypes := make([]StreamType, len(g.StreamTypes))
	for i, st := range g.StreamTypes {
		streamTypes[i] = StreamType(st)
	}

	var recordId *string
	if g.CurrentRecordId != "" {
		s := g.CurrentRecordId
		recordId = &s
	}

	return Peer{
		Name:             g.Name,
		SocketId:         sockets.SocketID(g.ID),
		LastPing:         lastPing,
		ConnectionsCount: g.ConnectionsCount,
		StreamTypes:      streamTypes,
		CurrentRecordId:  recordId,
	}
}

func ToApiPeers(grabbers []domain.Grabber) []Peer {
	peers := make([]Peer, len(grabbers))
	for i, g := range grabbers {
		peers[i] = ToApiPeer(g)
	}
	return peers
}
