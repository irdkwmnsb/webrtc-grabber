package service

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
)

type RecordingService struct {
	grabberRepo      domain.GrabberRepository
	commandSender    domain.GrabberCommandSender
	defaultTimeout   uint
	maxTimeout       uint
	recordingEnabled bool
}

func NewRecordingService(
	repo domain.GrabberRepository,
	commandSender domain.GrabberCommandSender,
	defaultTimeout uint,
	maxTimeout uint,
	recordingEnabled bool,
) *RecordingService {
	return &RecordingService{
		grabberRepo:      repo,
		commandSender:    commandSender,
		defaultTimeout:   defaultTimeout,
		maxTimeout:       maxTimeout,
		recordingEnabled: recordingEnabled,
	}
}

func (s *RecordingService) StartRecording(cmd domain.StartRecordingCommand) error {
	if !s.recordingEnabled {
		return domain.ErrRecordingNotSupported
	}

	grabber, err := s.grabberRepo.GetByName(cmd.GrabberName)
	if err != nil {
		return fmt.Errorf("grabber not found: %w", err)
	}

	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = s.defaultTimeout
	} else if timeout > s.maxTimeout {
		timeout = s.maxTimeout
	}

	return s.commandSender.SendStartRecording(grabber.ID, cmd.RecordID, timeout)
}

func (s *RecordingService) StopRecording(cmd domain.StopRecordingCommand) error {
	grabber, err := s.grabberRepo.GetByName(cmd.GrabberName)
	if err != nil {
		return fmt.Errorf("grabber not found: %w", err)
	}

	return s.commandSender.SendStopRecording(grabber.ID, cmd.RecordID)
}

func (s *RecordingService) UploadRecording(cmd domain.UploadRecordingCommand) error {
	grabber, err := s.grabberRepo.GetByName(cmd.GrabberName)
	if err != nil {
		return fmt.Errorf("grabber not found: %w", err)
	}

	return s.commandSender.SendUploadRecording(grabber.ID, cmd.RecordID)
}

func (s *RecordingService) DisconnectPlayers(cmd domain.DisconnectPlayersCommand) error {
	grabber, err := s.grabberRepo.GetByName(cmd.GrabberName)
	if err != nil {
		return fmt.Errorf("grabber not found: %w", err)
	}

	return s.commandSender.SendDisconnectPlayers(grabber.ID)
}
