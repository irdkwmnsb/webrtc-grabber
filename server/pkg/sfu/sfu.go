package sfu

import (
	"github.com/irdkwmnsb/webrtc-grabber/pkg/api"
	"github.com/irdkwmnsb/webrtc-grabber/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type NewSubscriberContext struct {
	publisherSocketID sockets.SocketID
	streamType        string
	c                 sockets.Socket
	offer             *webrtc.SessionDescription
	publisherConn     sockets.Socket
}

func CreateNewSubscriberContext(publisherSocketID sockets.SocketID,
	streamType string,
	c sockets.Socket,
	offer *webrtc.SessionDescription,
	publisherConn sockets.Socket) *NewSubscriberContext {

	return &NewSubscriberContext{
		publisherSocketID: publisherSocketID,
		streamType:        streamType,
		c:                 c,
		offer:             offer,
		publisherConn:     publisherConn,
	}
}

type SFU interface {
	Close()
	DeleteSubscriber(id sockets.SocketID)
	AddSubscriber(id sockets.SocketID, ctx *NewSubscriberContext) *api.PlayerMessage
	SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit)
	DeletePublisher(id sockets.SocketID)
	OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription)
	AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit)
}
