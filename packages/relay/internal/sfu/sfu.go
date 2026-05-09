package sfu

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type EventKind int

const (
	EventPublisherOffer EventKind = iota + 1
	EventPublisherICECandidate
	EventSubscriberICECandidate
)

func (kind EventKind) Critical() bool { return kind == EventPublisherOffer }

type Event struct {
	Kind         EventKind
	PublisherID  sockets.SocketID
	StreamType   string
	SubscriberID sockets.SocketID
	SDP          *api.SDP
	ICE          *api.ICECandidate
}

func PublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

type SFU interface {
	Close()

	Subscribe(
		subscriberID, publisherID sockets.SocketID,
		streamType string,
		offer api.SDP,
	) (api.SDP, error)
	Unsubscribe(subscriberID sockets.SocketID)
	SubscriberICE(subscriberID sockets.SocketID, candidate api.ICECandidate)

	PublisherAnswer(publisherKey string, answer api.SDP)
	PublisherICE(publisherKey string, candidate api.ICECandidate)
	DropPublisher(publisherID sockets.SocketID)

	NumEventShards() int
	EventShard(index int) <-chan Event
}
