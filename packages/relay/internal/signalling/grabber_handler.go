package signalling

import (
	"log/slog"

	"github.com/gofiber/contrib/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/service"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type GrabberHandler struct {
	config            *config.AppConfig
	grabberService    *service.GrabberService
	signallingService *service.SignallingService
	sessionHandler    *SessionHandler
}

func NewGrabberHandler(
	cfg *config.AppConfig,
	grabberService *service.GrabberService,
	signallingService *service.SignallingService,
	sessionHandler *SessionHandler,
) *GrabberHandler {
	return &GrabberHandler{
		config:            cfg,
		grabberService:    grabberService,
		signallingService: signallingService,
		sessionHandler:    sessionHandler,
	}
}

func (h *GrabberHandler) HandleSocket(c *websocket.Conn, peerName string) {
	session := h.sessionHandler.RegisterGrabberSession(c, peerName)
	defer session.Cleanup()
	defer h.signallingService.RemoveGrabber(string(session.SocketID))

	_ = h.grabberService.Register(string(session.SocketID), peerName)

	if err := session.Socket.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventInitPeer,
		InitPeer: &api.GrabberInitPeerMessage{
			PcConfigMessage: api.PcConfigMessage{PcConfig: h.config.WebRTC.PeerConnectionConfig},
			PingInterval:    h.config.Server.GrabberPingInterval,
		},
	}); err != nil {
		slog.Error("failed to send init_peer", "socketID", session.SocketID)
		return
	}

	var message api.GrabberMessage
	for {
		if err := session.Socket.ReadJSON(&message); err != nil {
			slog.Debug("grabber disconnected", "socketID", session.SocketID)
			break
		}

		answer := h.processMessage(session.SocketID, message)
		if answer != nil {
			_ = session.Socket.WriteJSON(answer)
		}
	}
}

func (h *GrabberHandler) processMessage(id sockets.SocketID, m api.GrabberMessage) *api.GrabberMessage {
	switch m.Event {
	case api.GrabberMessageEventPing:
		h.handlePing(id, m)
	case api.GrabberMessageEventOfferAnswer:
		h.handleOfferAnswer(id, m)
	case api.GrabberMessageEventGrabberIce:
		h.handleICE(id, m)
	}
	return nil
}

func (h *GrabberHandler) handlePing(id sockets.SocketID, m api.GrabberMessage) {
	if m.Ping == nil {
		return
	}

	streamTypes := make([]string, len(m.Ping.StreamTypes))
	for i, st := range m.Ping.StreamTypes {
		streamTypes[i] = string(st)
	}

	recordID := ""
	if m.Ping.CurrentRecordId != nil {
		recordID = *m.Ping.CurrentRecordId
	}

	_ = h.grabberService.UpdateHeartbeat(string(id), m.Ping.ConnectionsCount, streamTypes, recordID)
}

func (h *GrabberHandler) handleOfferAnswer(id sockets.SocketID, m api.GrabberMessage) {
	if m.OfferAnswer == nil || m.OfferAnswer.PeerId == "" {
		return
	}

	publisherKey := m.OfferAnswer.PeerId
	prefix := string(id) + "_"
	if len(publisherKey) <= len(prefix) || publisherKey[:len(prefix)] != prefix {
		slog.Warn("invalid PeerId in OfferAnswer", "peerId", publisherKey)
		return
	}

	streamType := publisherKey[len(prefix):]
	_ = h.signallingService.SetGrabberAnswer(string(id), streamType, m.OfferAnswer.Answer)
}

func (h *GrabberHandler) handleICE(id sockets.SocketID, m api.GrabberMessage) {
	if m.Ice == nil || m.Ice.PeerId == nil {
		return
	}

	publisherKey := *m.Ice.PeerId
	prefix := string(id) + "_"
	if len(publisherKey) <= len(prefix) || publisherKey[:len(prefix)] != prefix {
		slog.Warn("invalid PeerId in GrabberIce", "peerId", publisherKey)
		return
	}

	streamType := publisherKey[len(prefix):]
	_ = h.signallingService.AddGrabberICE(string(id), streamType, m.Ice.Candidate)
}
