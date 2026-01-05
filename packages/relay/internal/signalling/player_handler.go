package signalling

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/service"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"github.com/pion/webrtc/v4"
)

const PlayerSendPeerStatusInterval = 5 * time.Second

type PlayerHandler struct {
	grabberService    *service.GrabberService
	signallingService *service.SignallingService
	sessionHandler    *SessionHandler
	grabberSockets    *sockets.SocketPool
}

func NewPlayerHandler(
	grabberService *service.GrabberService,
	signallingService *service.SignallingService,
	sessionHandler *SessionHandler,
	grabberSockets *sockets.SocketPool,
) *PlayerHandler {
	return &PlayerHandler{
		grabberService:    grabberService,
		signallingService: signallingService,
		sessionHandler:    sessionHandler,
		grabberSockets:    grabberSockets,
	}
}

func (h *PlayerHandler) HandleAdminSocket(c *websocket.Conn) {
	session := h.sessionHandler.RegisterPlayerSession(c)
	defer session.Cleanup()

	sendPeerStatus := func() {
		allActive, _ := h.grabberService.GetAllActive()
		allParticipants, _ := h.grabberService.GetAllParticipantsStatus()
		_ = session.Socket.WriteJSON(api.PlayerMessage{
			Event:              api.PlayerMessageEventPeerStatus,
			PeersStatus:        api.ToApiPeers(allActive),
			ParticipantsStatus: api.ToApiPeers(allParticipants),
		})
	}

	sendPeerStatus()
	timer := utils.SetIntervalTimer(PlayerSendPeerStatusInterval, sendPeerStatus)
	defer timer.Stop()

	var message api.PlayerMessage
	for {
		if err := session.Socket.ReadJSON(&message); err != nil {
			slog.Debug("player admin disconnected", "socketID", session.SocketID)
			break
		}

		answer := h.processMessage(session.Socket, session.SocketID, message)
		if answer != nil {
			_ = session.Socket.WriteJSON(answer)
		}
	}
}

func (h *PlayerHandler) HandlePlaySocket(c *websocket.Conn) {
	session := h.sessionHandler.RegisterPlayerSession(c)
	defer session.Cleanup()
	defer h.signallingService.RemovePlayer(string(session.SocketID))

	loop := NewPlayerConnectionLoop(session.Socket, session.SocketID)
	loop.Start()
	defer loop.Stop()

	var message api.PlayerMessage
	for {
		if err := session.Socket.ReadJSON(&message); err != nil {
			slog.Debug("player disconnected", "socketID", session.SocketID)
			break
		}

		answer := h.processMessage(session.Socket, session.SocketID, message)
		if answer != nil {
			loop.SendMessage(answer)
		}
	}
}

func (h *PlayerHandler) processMessage(c sockets.Socket, id sockets.SocketID, m api.PlayerMessage) *api.PlayerMessage {
	switch m.Event {
	case api.PlayerMessageEventPong:
		return nil

	case api.PlayerMessageEventOffer:
		h.handleOffer(c, id, m)
		return nil

	case api.PlayerMessageEventPlayerIce:
		if m.Ice != nil {
			_ = h.signallingService.AddPlayerICE(string(id), m.Ice.Candidate)
		}
		return nil
	}
	return nil
}

func (h *PlayerHandler) handleOffer(c sockets.Socket, id sockets.SocketID, m api.PlayerMessage) {
	if m.Offer == nil {
		_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		return
	}

	peerName := h.resolvePeerName(m.Offer)
	if peerName == "" {
		_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		slog.Warn("offer missing peer identifier")
		return
	}

	grabber, err := h.grabberService.GetGrabberByName(peerName)
	if err != nil {
		_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		return
	}

	grabberConn := h.grabberSockets.GetSocket(sockets.SocketID(grabber.ID))
	if grabberConn == nil {
		_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		return
	}

	onPlayerICE := func(candidate webrtc.ICECandidateInit, targetID string) error {
		return c.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventGrabberIce,
			Ice:   &api.IceMessage{PeerId: &targetID, Candidate: candidate},
		})
	}

	onGrabberOffer := func(offer webrtc.SessionDescription, targetID string) error {
		return grabberConn.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventOffer,
			Offer: &api.OfferMessage{
				Offer:      offer,
				PeerId:     &targetID,
				StreamType: m.Offer.StreamType,
			},
		})
	}

	onGrabberICE := func(candidate webrtc.ICECandidateInit, targetID string) error {
		return grabberConn.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventPlayerIce,
			Ice:   &api.IceMessage{PeerId: &targetID, Candidate: candidate},
		})
	}

	answer, err := h.signallingService.Subscribe(
		string(id), peerName, m.Offer.StreamType, m.Offer.Offer,
		onPlayerICE, onGrabberOffer, onGrabberICE,
	)

	if err != nil {
		slog.Error("subscription failed", "error", err)
		_ = c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed})
		return
	}

	publisherKey := fmt.Sprintf("%s_%s", grabber.ID, m.Offer.StreamType)
	_ = c.WriteJSON(api.PlayerMessage{
		Event:       api.PlayerMessageEventOfferAnswer,
		OfferAnswer: &api.OfferAnswerMessage{PeerId: publisherKey, Answer: answer},
	})
}

func (h *PlayerHandler) resolvePeerName(offer *api.OfferMessage) string {
	if offer.PeerName != nil {
		return *offer.PeerName
	}
	if offer.PeerId != nil {
		if g, err := h.grabberService.GetGrabber(*offer.PeerId); err == nil {
			return g.Name
		}
	}
	return ""
}
