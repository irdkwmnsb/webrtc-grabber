package sfu

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type Callback = func(pc *webrtc.PeerConnection) error

type PublisherCallbacks struct {
	OnOffer        func(offer webrtc.SessionDescription, publisherKey string)
	OnICECandidate func(candidate webrtc.ICECandidateInit, publisherKey string)
}

func PublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

type SFU interface {
	Close()
	DeleteSubscriber(id sockets.SocketID)
	AddSubscriber(id, publisherSocketID sockets.SocketID, streamType string, callback Callback, publisherCallbacks PublisherCallbacks) error
	SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit)
	DeletePublisher(id sockets.SocketID)
	OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription)
	AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit)
}
