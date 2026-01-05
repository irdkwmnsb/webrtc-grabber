package domain

import "github.com/pion/webrtc/v4"

type ICECallback func(candidate webrtc.ICECandidateInit, targetID string) error
type SDPCallback func(sdp webrtc.SessionDescription, targetID string) error

type MediaProvider interface {
	Subscribe(
		subscriberID string,
		grabberID string,
		streamType string,
		offer webrtc.SessionDescription,
		onPlayerICE ICECallback,
		onGrabberOffer SDPCallback,
		onGrabberICE ICECallback,
	) (webrtc.SessionDescription, error)
	AddSubscriberICE(subscriberID string, candidate webrtc.ICECandidateInit) error
	AddPublisherICE(grabberID string, streamType string, candidate webrtc.ICECandidateInit) error
	SetPublisherAnswer(grabberID string, streamType string, answer webrtc.SessionDescription) error
	RemoveSubscriber(subscriberID string)
	RemovePublisher(grabberID string)
	Close()
}
