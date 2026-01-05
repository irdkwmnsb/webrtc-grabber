package domain

import "errors"

var (
	ErrRecordingNotSupported = errors.New("recording storage is not configured")
	ErrGrabberOffline        = errors.New("grabber is offline")
)

type RecordingCommand interface {
	GetGrabberName() string
}

type StartRecordingCommand struct {
	GrabberName string
	RecordID    string
	Timeout     uint
}

func (c StartRecordingCommand) GetGrabberName() string {
	return c.GrabberName
}

type StopRecordingCommand struct {
	GrabberName string
	RecordID    string
}

func (c StopRecordingCommand) GetGrabberName() string {
	return c.GrabberName
}

type UploadRecordingCommand struct {
	GrabberName string
	RecordID    string
}

func (c UploadRecordingCommand) GetGrabberName() string {
	return c.GrabberName
}

type DisconnectPlayersCommand struct {
	GrabberName string
}

func (c DisconnectPlayersCommand) GetGrabberName() string {
	return c.GrabberName
}

type GrabberCommandSender interface {
	SendStartRecording(grabberID string, recordID string, timeoutMsec uint) error
	SendStopRecording(grabberID string, recordID string) error
	SendUploadRecording(grabberID string, recordID string) error
	SendDisconnectPlayers(grabberID string) error
}
