package signalling

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

// WebSocketCommandSender implements domain.GrabberCommandSender using WebSocket connections.
type WebSocketCommandSender struct {
	grabberSockets *sockets.SocketPool
}

// NewWebSocketCommandSender creates a new WebSocket-based command sender.
func NewWebSocketCommandSender(grabberSockets *sockets.SocketPool) *WebSocketCommandSender {
	return &WebSocketCommandSender{
		grabberSockets: grabberSockets,
	}
}

// SendStartRecording sends a command to start recording.
func (w *WebSocketCommandSender) SendStartRecording(grabberID string, recordID string, timeoutMsec uint) error {
	socket := w.grabberSockets.GetSocket(sockets.SocketID(grabberID))
	if socket == nil {
		return domain.ErrGrabberOffline
	}

	err := socket.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventRecordStart,
		RecordStart: &api.RecordStartMessage{
			RecordId:    recordID,
			TimeoutMsec: timeoutMsec,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to send start recording command: %w", err)
	}
	return nil
}

// SendStopRecording sends a command to stop recording.
func (w *WebSocketCommandSender) SendStopRecording(grabberID string, recordID string) error {
	socket := w.grabberSockets.GetSocket(sockets.SocketID(grabberID))
	if socket == nil {
		return domain.ErrGrabberOffline
	}

	err := socket.WriteJSON(api.GrabberMessage{
		Event:      api.GrabberMessageEventRecordStop,
		RecordStop: &api.RecordStopMessage{RecordId: recordID},
	})
	if err != nil {
		return fmt.Errorf("failed to send stop recording command: %w", err)
	}
	return nil
}

// SendUploadRecording sends a command to upload a recording.
func (w *WebSocketCommandSender) SendUploadRecording(grabberID string, recordID string) error {
	socket := w.grabberSockets.GetSocket(sockets.SocketID(grabberID))
	if socket == nil {
		return domain.ErrGrabberOffline
	}

	err := socket.WriteJSON(api.GrabberMessage{
		Event:        api.GrabberMessageEventRecordUpload,
		RecordUpload: &api.RecordUploadMessage{RecordId: recordID},
	})
	if err != nil {
		return fmt.Errorf("failed to send upload recording command: %w", err)
	}
	return nil
}

// SendDisconnectPlayers sends a command to disconnect all players.
func (w *WebSocketCommandSender) SendDisconnectPlayers(grabberID string) error {
	socket := w.grabberSockets.GetSocket(sockets.SocketID(grabberID))
	if socket == nil {
		return domain.ErrGrabberOffline
	}

	err := socket.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventPlayersDisconnect,
	})
	if err != nil {
		return fmt.Errorf("failed to send disconnect players command: %w", err)
	}
	return nil
}
