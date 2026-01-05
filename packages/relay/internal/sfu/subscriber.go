package sfu

import "github.com/pion/webrtc/v4"

type Subscriber struct {
	pc           *webrtc.PeerConnection
	publisherKey string
}

func NewSubscriber(pc *webrtc.PeerConnection, publisherKey string) *Subscriber {
	return &Subscriber{
		pc:           pc,
		publisherKey: publisherKey,
	}
}
