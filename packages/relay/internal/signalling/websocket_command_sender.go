package signalling

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type WebSocketCommandSender struct {
	grabberSockets *sockets.SocketPool
}

func NewWebSocketCommandSender(grabberSockets *sockets.SocketPool) *WebSocketCommandSender {
	return &WebSocketCommandSender{
		grabberSockets: grabberSockets,
	}
}

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
