package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/fasthttp/websocket"
	"github.com/pion/webrtc/v4"

	relayapi "github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sdpconv"
)

func runSubscriber(ctx context.Context, wsBase string, idx int, peerName string) error {
	url := wsBase + "/ws/player/play"
	rawWS, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("ws dial %s: %w", url, err)
	}
	defer rawWS.Close()

	go func() {
		<-ctx.Done()
		_ = rawWS.Close()
	}()

	ws := newWSWriter(rawWS)

	var msg relayapi.PlayerMessage
	if err := rawWS.ReadJSON(&msg); err != nil {
		return fmt.Errorf("read auth_request: %w", err)
	}
	if msg.Event != relayapi.PlayerMessageEventAuthRequest {
		return fmt.Errorf("expected auth:request, got %s", msg.Event)
	}

	if err := ws.writeJSON(relayapi.PlayerMessage{
		Event:      relayapi.PlayerMessageEventAuth,
		PlayerAuth: &relayapi.PlayerAuthMessage{Credential: *credential},
	}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	if err := rawWS.ReadJSON(&msg); err != nil {
		return fmt.Errorf("read init_peer: %w", err)
	}
	if msg.Event != relayapi.PlayerMessageEventInitPeer || msg.InitPeer == nil {
		return fmt.Errorf("expected init_peer, got %s", msg.Event)
	}

	wapi := buildPionAPI()
	pc, err := wapi.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return fmt.Errorf("new pc: %w", err)
	}
	defer pc.Close()

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return fmt.Errorf("add video transceiver: %w", err)
	}
	if *streamType == "webcam" {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			return fmt.Errorf("add audio transceiver: %w", err)
		}
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			stats.rxPackets.Add(1)
			stats.rxBytes.Add(int64(len(pkt.Payload)))
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		_ = ws.writeJSON(relayapi.PlayerMessage{
			Event: relayapi.PlayerMessageEventPlayerIce,
			Ice: &relayapi.IceMessage{
				PeerName:  &peerName,
				Candidate: sdpconv.FromPionICE(c.ToJSON()),
			},
		})
	})

	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		slog.Debug("subscriber ICE state", "id", idx, "peer", peerName, "state", s.String())
		if s == webrtc.ICEConnectionStateConnected {
			stats.subsReady.Add(1)
		}
		if s == webrtc.ICEConnectionStateFailed || s == webrtc.ICEConnectionStateDisconnected {
			stats.subsReady.Add(-1)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set local: %w", err)
	}

	if err := ws.writeJSON(relayapi.PlayerMessage{
		Event: relayapi.PlayerMessageEventOffer,
		Offer: &relayapi.OfferMessage{
			Offer:      sdpconv.FromPionSDP(offer),
			PeerName:   &peerName,
			StreamType: *streamType,
		},
	}); err != nil {
		return fmt.Errorf("send offer: %w", err)
	}

	for {
		var m relayapi.PlayerMessage
		if err := rawWS.ReadJSON(&m); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}
		switch m.Event {
		case relayapi.PlayerMessageEventOfferAnswer:
			if m.OfferAnswer == nil {
				continue
			}
			if err := pc.SetRemoteDescription(sdpconv.ToPionSDP(m.OfferAnswer.Answer)); err != nil {
				return fmt.Errorf("set remote: %w", err)
			}
		case relayapi.PlayerMessageEventGrabberIce:
			if m.Ice == nil {
				continue
			}
			if err := pc.AddICECandidate(sdpconv.ToPionICE(m.Ice.Candidate)); err != nil {
				slog.Debug("subscriber add ice failed", "id", idx, "err", err)
			}
		case relayapi.PlayerMessageEventOfferFailed:
			return errors.New("offer rejected by server")
		}
	}
}
