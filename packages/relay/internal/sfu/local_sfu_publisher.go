package sfu

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

func (sfu *LocalSFU) ensurePublisher(
	publisherKey string,
	publisherSocketID sockets.SocketID,
	streamType string,
	onOffer domain.SDPCallback,
	onICE domain.ICECallback,
) error {
	publisher, loaded := sfu.publishers.LoadOrStore(publisherKey, NewPublisher())
	if loaded {
		if publisher.IsSetupInProgress() {
			select {
			case <-publisher.WaitSetup():
				return nil
			case <-time.After(PublisherWaitingTime):
				return fmt.Errorf("timeout waiting for setup")
			}
		}
		return nil
	}

	if !publisher.StartSetup() {
		select {
		case <-publisher.WaitSetup():
			return nil
		case <-time.After(PublisherWaitingTime):
			return fmt.Errorf("timeout waiting for setup")
		}
	}

	go sfu.setupPublisherConnection(publisherSocketID, publisher, streamType, onOffer, onICE)

	select {
	case <-publisher.WaitSetup():
		return nil
	case <-time.After(PublisherWaitingTime):
		sfu.cleanupPublisher(publisherKey)
		return fmt.Errorf("timeout during setup")
	}
}

func (sfu *LocalSFU) setupPublisherConnection(
	publisherSocketID sockets.SocketID,
	publisher *Publisher,
	streamType string,
	onOffer domain.SDPCallback,
	onICE domain.ICECallback,
) {
	publisherKey := getPublisherKey(publisherSocketID, streamType)

	defer func() {
		if r := recover(); r != nil {
			slog.Error("PANIC in setupPublisherConnection", "panic", r, "stack", string(debug.Stack()))
			publisher.FinishSetup()
			sfu.cleanupPublisher(publisherKey)
		}
	}()

	defer publisher.FinishSetup()

	pc, err := sfu.createPublisherPC(publisherSocketID, publisherKey)
	if err != nil {
		sfu.cleanupPublisher(publisherKey)
		return
	}

	publisher.pc = pc

	if err := sfu.configurePublisherTransceivers(pc, streamType, publisherSocketID); err != nil {
		sfu.cleanupPublisher(publisherKey)
		return
	}

	sfu.setupPublisherTrackHandler(pc, publisher, publisherSocketID)

	sfu.setupPublisherICEHandlers(pc, publisherKey, onICE)

	if err := sfu.sendOfferToPublisher(pc, publisherSocketID, publisherKey, onOffer); err != nil {
		sfu.cleanupPublisher(publisherKey)
		return
	}

	select {
	case <-publisher.WaitSetup():
		slog.Info("publisher setup completed", "socketID", publisherSocketID, "tracks", publisher.BroadcasterCount())
	case <-time.After(PublisherWaitingTime):
		slog.Warn("timeout waiting for tracks", "socketID", publisherSocketID)
		sfu.cleanupPublisher(publisherKey)
	}
}

func (sfu *LocalSFU) createPublisherPC(socketID sockets.SocketID, publisherKey string) (*webrtc.PeerConnection, error) {
	pc, err := sfu.api.NewPeerConnection(sfu.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		slog.Error("failed to create publisher peer connection", "socketID", socketID, "error", err)
		return nil, err
	}

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		if state == webrtc.ICEConnectionStateClosed || state == webrtc.ICEConnectionStateFailed {
			sfu.cleanupPublisher(publisherKey)
		}
	})

	return pc, nil
}

func (sfu *LocalSFU) configurePublisherTransceivers(pc *webrtc.PeerConnection, streamType string, socketID sockets.SocketID) error {
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		slog.Error("failed to add video transceiver", "socketID", socketID, "error", err)
		return err
	}

	if streamType == "webcam" {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			slog.Error("failed to add audio transceiver", "socketID", socketID, "error", err)
			return err
		}
	}

	return nil
}

func (sfu *LocalSFU) setupPublisherTrackHandler(pc *webrtc.PeerConnection, publisher *Publisher, socketID sockets.SocketID) {
	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		if sfu.config.DisableAudio && remoteTrack.Kind() == webrtc.RTPCodecTypeAudio {
			return
		}

		broadcaster, err := NewTrackBroadcaster(remoteTrack, socketID)
		if err != nil {
			slog.Error("failed to create broadcaster", "socketID", socketID, "error", err)
			return
		}

		publisher.AddBroadcaster(broadcaster)

		for _, subscriberPC := range publisher.GetSubscribers() {
			sfu.addTrackToExistingSubscriber(subscriberPC, broadcaster, publisher.pc)
		}

		if publisher.BroadcasterCount() >= publisher.GetExpectedTracks() {
			publisher.FinishSetup()
		}
	})
}

func (sfu *LocalSFU) addTrackToExistingSubscriber(subscriberPC *webrtc.PeerConnection, broadcaster *TrackBroadcaster, publisherPC *webrtc.PeerConnection) {
	localTrack := broadcaster.GetLocalTrack()
	rtpSender, err := subscriberPC.AddTrack(localTrack)
	if err != nil {
		slog.Error("failed to add track to existing subscriber", "error", err)
		return
	}

	go sfu.processRTCPFeedback(rtpSender, publisherPC, broadcaster.GetRemoteSSRC())
	broadcaster.AddSubscriber(subscriberPC)

	trackType := "video"
	if localTrack.Kind() == webrtc.RTPCodecTypeAudio {
		trackType = "audio"
	}
	metrics.TracksAddedTotal.WithLabelValues(trackType).Inc()
	metrics.ActiveTracks.WithLabelValues(trackType).Inc()
}

func (sfu *LocalSFU) setupPublisherICEHandlers(pc *webrtc.PeerConnection, publisherKey string, onICE domain.ICECallback) {
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = onICE(candidate.ToJSON(), publisherKey)
		}
	})
}

func (sfu *LocalSFU) sendOfferToPublisher(pc *webrtc.PeerConnection, socketID sockets.SocketID, publisherKey string, onOffer domain.SDPCallback) error {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		slog.Error("failed to create offer", "socketID", socketID, "error", err)
		return err
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		slog.Error("failed to set local description", "socketID", socketID, "error", err)
		return err
	}

	if err := onOffer(offer, publisherKey); err != nil {
		slog.Error("failed to send offer", "socketID", socketID, "error", err)
		return err
	}

	return nil
}
