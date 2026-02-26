package signalling

import (
	"fmt"
	"log/slog"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

func completeSignaling(pc *webrtc.PeerConnection, offer *webrtc.SessionDescription,
	id sockets.SocketID, streamType string, c sockets.Socket, publisherKey string) (*webrtc.SessionDescription, error) {

	subscriberPeerID := fmt.Sprintf("%s_%s", id, streamType)

	if err := pc.SetRemoteDescription(*offer); err != nil {
		return nil, fmt.Errorf("failed to set remote description for %s: %w", subscriberPeerID, err)
	}

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			metrics.ICECandidatesTotal.WithLabelValues(candidate.Typ.String()).Inc()
			_ = c.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventGrabberIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			})
		}
	})

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create answer for %s: %w", subscriberPeerID, err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("failed to set local description for %s: %w", subscriberPeerID, err)
	}

	return &answer, nil
}

func createOnOfferCallback(conn sockets.Socket, streamType string) func(webrtc.SessionDescription, string) {
	return func(offer webrtc.SessionDescription, key string) {
		if err := conn.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventOffer,
			Offer: &api.OfferMessage{
				Offer:      offer,
				PeerId:     &key,
				StreamType: streamType,
			},
		}); err != nil {
			slog.Error("failed to send offer to grabber", "error", err)
		}
	}
}

func createOnICECandidateCallback(conn sockets.Socket) func(candidate webrtc.ICECandidateInit, key string) {
	return func(candidate webrtc.ICECandidateInit, key string) {
		if err := conn.WriteJSON(api.GrabberMessage{
			Event: api.GrabberMessageEventPlayerIce,
			Ice: &api.IceMessage{
				PeerId:    &key,
				Candidate: candidate,
			},
		}); err != nil {
			slog.Error("failed to send ICE candidate to grabber", "error", err)
		}
	}
}

func createSubscriberCallback(msg api.PlayerMessage, id sockets.SocketID, conn sockets.Socket, streamType, publisherKey string) func(pc *webrtc.PeerConnection) error {
	return func(pc *webrtc.PeerConnection) error {
		answer, err := completeSignaling(pc, &msg.Offer.Offer, id, streamType, conn, publisherKey)
		if err != nil {
			return err
		}
		return conn.WriteJSON(api.PlayerMessage{
			Event: api.PlayerMessageEventOfferAnswer,
			OfferAnswer: &api.OfferAnswerMessage{
				PeerId: publisherKey,
				Answer: *answer,
			},
		})
	}
}
