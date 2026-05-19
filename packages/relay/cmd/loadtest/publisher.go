package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	relayapi "github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sdpconv"
)

func runPublisher(ctx context.Context, wsBase, peerName string, ready chan<- string) error {
	url := wsBase + "/ws/peers/" + peerName
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

	// First message from the relay must be init_peer.
	var init relayapi.GrabberMessage
	if err := rawWS.ReadJSON(&init); err != nil {
		return fmt.Errorf("read init_peer: %w", err)
	}
	if init.Event != relayapi.GrabberMessageEventInitPeer || init.InitPeer == nil {
		return fmt.Errorf("expected init_peer, got %s", init.Event)
	}

	// Announce supported stream types via the same ping the real grabber
	// sends — otherwise the relay rejects subscriber offers with
	// "no such stream type in grabber".
	if err := sendPing(ws, *streamType); err != nil {
		return fmt.Errorf("send initial ping: %w", err)
	}
	go pingLoop(ctx, ws, *streamType)

	stats.pubsReady.Add(1)
	defer stats.pubsReady.Add(-1)
	slog.Debug("publisher registered", "peer", peerName)
	select {
	case ready <- peerName:
	case <-ctx.Done():
		return ctx.Err()
	}

	wapi := buildPionAPI()

	var (
		pc           *webrtc.PeerConnection
		publisherKey string
	)
	defer func() {
		if pc != nil {
			_ = pc.Close()
		}
	}()

	for {
		var msg relayapi.GrabberMessage
		if err := rawWS.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}

		switch msg.Event {
		case relayapi.GrabberMessageEventOffer:
			if msg.Offer == nil || msg.Offer.PeerId == nil {
				continue
			}
			if pc != nil {
				slog.Debug("ignoring extra offer for existing publisher", "peer", peerName)
				continue
			}
			publisherKey = *msg.Offer.PeerId
			newPc, err := setupPublisherPC(ctx, wapi, sdpconv.ToPionSDP(msg.Offer.Offer), ws, publisherKey, peerName)
			if err != nil {
				return fmt.Errorf("setup publisher pc: %w", err)
			}
			pc = newPc

		case relayapi.GrabberMessageEventPlayerIce:
			if msg.Ice == nil || pc == nil {
				continue
			}
			if err := pc.AddICECandidate(sdpconv.ToPionICE(msg.Ice.Candidate)); err != nil {
				slog.Debug("publisher add ice failed", "peer", peerName, "err", err)
			}
		}
	}
}

func setupPublisherPC(
	ctx context.Context,
	wapi *webrtc.API,
	offer webrtc.SessionDescription,
	ws *wsWriter,
	publisherKey, peerName string,
) (*webrtc.PeerConnection, error) {
	pc, err := wapi.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}

	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "loadbot-"+peerName)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}

	// Track must be added before SetRemoteDescription so the local sendonly
	// transceiver pairs with the SFU's recvonly m-line.
	if _, err := pc.AddTrack(videoTrack); err != nil {
		_ = pc.Close()
		return nil, err
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		_ = ws.writeJSON(relayapi.GrabberMessage{
			Event: relayapi.GrabberMessageEventGrabberIce,
			Ice: &relayapi.IceMessage{
				PeerId:    &publisherKey,
				Candidate: sdpconv.FromPionICE(c.ToJSON()),
			},
		})
	})

	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		slog.Debug("publisher ICE state", "peer", peerName, "state", s.String())
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("set remote: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("set local: %w", err)
	}

	if err := ws.writeJSON(relayapi.GrabberMessage{
		Event: relayapi.GrabberMessageEventOfferAnswer,
		OfferAnswer: &relayapi.OfferAnswerMessage{
			PeerId: publisherKey,
			Answer: sdpconv.FromPionSDP(answer),
		},
	}); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("send offer_answer: %w", err)
	}

	go feedSyntheticVP8(ctx, videoTrack, peerName)
	return pc, nil
}

func feedSyntheticVP8(ctx context.Context, track *webrtc.TrackLocalStaticSample, peerName string) {
	frameInterval := time.Second / time.Duration(*fps)
	bytesPerFrame := max((*videoKbps*1000)/(8**fps), 64)
	buf := make([]byte, bytesPerFrame)

	t := time.NewTicker(frameInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := rand.Read(buf); err != nil {
				continue
			}
			if err := track.WriteSample(media.Sample{Data: buf, Duration: frameInterval}); err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return
				}
				slog.Debug("write sample", "peer", peerName, "err", err)
				return
			}
			stats.txPackets.Add(1)
			stats.txBytes.Add(int64(len(buf)))
		}
	}
}
