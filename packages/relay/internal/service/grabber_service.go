package service

import (
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
)

type GrabberService struct {
	repo                 domain.GrabberRepository
	expectedParticipants []string
}

func NewGrabberService(repo domain.GrabberRepository, participants []string) *GrabberService {
	return &GrabberService{
		repo:                 repo,
		expectedParticipants: participants,
	}
}

func (s *GrabberService) Register(id string, name string) error {
	grabber := domain.Grabber{
		ID:       id,
		Name:     name,
		LastPing: time.Now(),
	}
	return s.repo.Save(grabber)
}

func (s *GrabberService) UpdateHeartbeat(id string, connectionsCount int, streamTypes []string, recordID string) error {
	grabber, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}

	grabber.LastPing = time.Now()
	grabber.ConnectionsCount = connectionsCount
	grabber.StreamTypes = streamTypes
	grabber.CurrentRecordId = recordID

	return s.repo.Save(grabber)
}

func (s *GrabberService) GetGrabber(id string) (domain.Grabber, error) {
	return s.repo.GetByID(id)
}

func (s *GrabberService) GetGrabberByName(name string) (domain.Grabber, error) {
	return s.repo.GetByName(name)
}

func (s *GrabberService) RemoveGrabber(id string) error {
	return s.repo.Delete(id)
}

func (s *GrabberService) CleanupStale(timeout time.Duration) error {
	return s.repo.DeleteStale(timeout)
}

func (s *GrabberService) GetAllParticipantsStatus() ([]domain.Grabber, error) {
	var result []domain.Grabber

	for _, name := range s.expectedParticipants {
		grabber, err := s.repo.GetByName(name)
		if err == nil {
			result = append(result, grabber)
		} else {
			result = append(result, domain.Grabber{Name: name})
		}
	}
	return result, nil
}

func (s *GrabberService) GetAllActive() ([]domain.Grabber, error) {
	return s.repo.GetAll()
}

func (s *GrabberService) UpdateParticipants(participants []string) {
	s.expectedParticipants = participants
}
