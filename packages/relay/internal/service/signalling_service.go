package service

import (
	"fmt"
	"slices"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/pion/webrtc/v4"
)

type SignallingService struct {
	mediaProvider domain.MediaProvider
	grabberRepo   domain.GrabberRepository
}

func NewSignallingService(mp domain.MediaProvider, repo domain.GrabberRepository) *SignallingService {
	return &SignallingService{
		mediaProvider: mp,
		grabberRepo:   repo,
	}
}

func (s *SignallingService) Subscribe(
	subscriberID string,
	grabberName string,
	streamType string,
	offer webrtc.SessionDescription,
	playerICECallback domain.ICECallback,
	grabberOfferCallback domain.SDPCallback,
	grabberICECallback domain.ICECallback,
) (webrtc.SessionDescription, error) {

	grabber, err := s.grabberRepo.GetByName(grabberName)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("grabber not found: %w", err)
	}

	if !slices.Contains(grabber.StreamTypes, streamType) {
		return webrtc.SessionDescription{}, fmt.Errorf("stream type %s not supported by grabber", streamType)
	}

	return s.mediaProvider.Subscribe(
		subscriberID,
		grabber.ID,
		streamType,
		offer,
		playerICECallback,
		grabberOfferCallback,
		grabberICECallback,
	)
}

func (s *SignallingService) AddPlayerICE(subscriberID string, candidate webrtc.ICECandidateInit) error {
	return s.mediaProvider.AddSubscriberICE(subscriberID, candidate)
}

func (s *SignallingService) AddGrabberICE(grabberID string, streamType string, candidate webrtc.ICECandidateInit) error {
	return s.mediaProvider.AddPublisherICE(grabberID, streamType, candidate)
}

func (s *SignallingService) SetGrabberAnswer(grabberID string, streamType string, answer webrtc.SessionDescription) error {
	return s.mediaProvider.SetPublisherAnswer(grabberID, streamType, answer)
}

func (s *SignallingService) RemovePlayer(subscriberID string) {
	s.mediaProvider.RemoveSubscriber(subscriberID)
}

func (s *SignallingService) RemoveGrabber(grabberID string) {
	s.mediaProvider.RemovePublisher(grabberID)
}
