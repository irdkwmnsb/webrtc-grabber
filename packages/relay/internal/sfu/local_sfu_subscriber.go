package sfu

import (
	"fmt"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func (sfu *LocalSFU) createSubscriberPC(id sockets.SocketID) (*webrtc.PeerConnection, error) {
	pc, err := sfu.api.NewPeerConnection(sfu.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		metrics.PeerConnectionFailuresTotal.WithLabelValues("subscriber_creation_failed").Inc()
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	metrics.ActivePeerConnections.Inc()
	metrics.PeerConnectionsCreatedTotal.Inc()

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			sfu.RemoveSubscriber(string(id))
		}
	})

	return pc, nil
}

func (sfu *LocalSFU) addTracksToSubscriber(subscriber *Subscriber, publisherKey string, id sockets.SocketID) error {
	publisher, ok := sfu.publishers.Load(publisherKey)
	if !ok {
		return fmt.Errorf("publisher not found: %s", publisherKey)
	}

	publisher.AddSubscriber(subscriber.pc)

	for _, broadcaster := range publisher.GetBroadcasters() {
		localTrack := broadcaster.GetLocalTrack()
		rtpSender, err := subscriber.pc.AddTrack(localTrack)
		if err != nil {
			return fmt.Errorf("failed to add track: %w", err)
		}

		go sfu.processRTCPFeedback(rtpSender, publisher.pc, broadcaster.GetRemoteSSRC())
		broadcaster.AddSubscriber(subscriber.pc)

		trackType := "unknown"
		if localTrack.Kind() == webrtc.RTPCodecTypeVideo {
			trackType = "video"
		} else if localTrack.Kind() == webrtc.RTPCodecTypeAudio {
			trackType = "audio"
		}
		metrics.TracksAddedTotal.WithLabelValues(trackType).Inc()
		metrics.ActiveTracks.WithLabelValues(trackType).Inc()
	}

	return nil
}

func (sfu *LocalSFU) completeSubscriberSignaling(
	pc *webrtc.PeerConnection,
	offer *webrtc.SessionDescription,
	onPlayerICE domain.ICECallback,
	publisherKey string,
) (*webrtc.SessionDescription, error) {
	if err := pc.SetRemoteDescription(*offer); err != nil {
		return nil, fmt.Errorf("failed to set remote description: %w", err)
	}

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = onPlayerICE(candidate.ToJSON(), publisherKey)
		}
	})

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create answer: %w", err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("failed to set local description: %w", err)
	}

	return &answer, nil
}

func (sfu *LocalSFU) processRTCPFeedback(
	sender *webrtc.RTPSender,
	publisherPC *webrtc.PeerConnection,
	remoteSSRC uint32,
) {
	rtcpBuf := make([]byte, 1500)

	for {
		n, _, err := sender.Read(rtcpBuf)
		if err != nil {
			return
		}

		packets, err := rtcp.Unmarshal(rtcpBuf[:n])
		if err != nil {
			continue
		}

		for _, pkt := range packets {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				if err := publisherPC.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{MediaSSRC: remoteSSRC},
				}); err != nil {
					return
				}
			}
		}
	}
}
