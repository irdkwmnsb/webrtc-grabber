package sfu

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v4"
)

type LocalSFU struct {
	publishers  *utils.SyncMapWrapper[string, *Publisher]
	subscribers *utils.SyncMapWrapper[string, *Subscriber]
	config      *config.WebRTCConfig
	api         *webrtc.API
	ctx         context.Context
	cancel      context.CancelFunc
}

func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

func NewLocalSFU(cfg *config.WebRTCConfig, publicIP string) (*LocalSFU, error) {
	debug.SetGCPercent(20)

	mediaEngine := &webrtc.MediaEngine{}
	for _, codec := range cfg.Codecs {
		if err := mediaEngine.RegisterCodec(codec.Params, codec.Type); err != nil {
			return nil, fmt.Errorf("failed to register codec: %w", err)
		}
	}

	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return nil, fmt.Errorf("failed to register default interceptors: %w", err)
	}

	internalPliFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		return nil, fmt.Errorf("failed to create PLI factory: %w", err)
	}
	interceptorRegistry.Add(internalPliFactory)

	se := webrtc.SettingEngine{}
	if len(cfg.PeerConnectionConfig.IceServers) == 0 && len(publicIP) > 0 {
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}

	if cfg.PortMin > 0 && cfg.PortMax > 0 {
		err = se.SetEphemeralUDPPortRange(cfg.PortMin, cfg.PortMax)
		if err != nil {
			return nil, fmt.Errorf("failed to set WebRTC port range: %w", err)
		}
	}

	webrtcApi := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
		webrtc.WithSettingEngine(se),
	)

	ctx, cancel := context.WithCancel(context.Background())

	return &LocalSFU{
		api:         webrtcApi,
		config:      cfg,
		ctx:         ctx,
		cancel:      cancel,
		publishers:  utils.NewSyncMapWrapper[string, *Publisher](),
		subscribers: utils.NewSyncMapWrapper[string, *Subscriber](),
	}, nil
}

func (sfu *LocalSFU) Close() {
	sfu.cancel()

	sfu.publishers.Range(func(key string, value *Publisher) bool {
		if value != nil {
			value.Close()
		}
		return true
	})
	sfu.publishers.Clear()

	sfu.subscribers.Range(func(key string, value *Subscriber) bool {
		if value != nil && value.pc != nil {
			_ = value.pc.Close()
		}
		return true
	})
	sfu.subscribers.Clear()
}

func (sfu *LocalSFU) Subscribe(
	subscriberID string,
	grabberID string,
	streamType string,
	offer webrtc.SessionDescription,
	onPlayerICE domain.ICECallback,
	onGrabberOffer domain.SDPCallback,
	onGrabberICE domain.ICECallback,
) (webrtc.SessionDescription, error) {
	publisherKey := getPublisherKey(sockets.SocketID(grabberID), streamType)

	if err := sfu.ensurePublisher(publisherKey, sockets.SocketID(grabberID), streamType, onGrabberOffer, onGrabberICE); err != nil {
		return webrtc.SessionDescription{}, err
	}

	subscriberPC, err := sfu.createSubscriberPC(sockets.SocketID(subscriberID))
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	subscriber := NewSubscriber(subscriberPC, publisherKey)
	sfu.subscribers.Store(subscriberID, subscriber)

	if err := sfu.addTracksToSubscriber(subscriber, publisherKey, sockets.SocketID(subscriberID)); err != nil {
		sfu.RemoveSubscriber(subscriberID)
		return webrtc.SessionDescription{}, err
	}

	answer, err := sfu.completeSubscriberSignaling(subscriberPC, &offer, onPlayerICE, publisherKey)
	if err != nil {
		sfu.RemoveSubscriber(subscriberID)
		return webrtc.SessionDescription{}, err
	}

	return *answer, nil
}

func (sfu *LocalSFU) RemoveSubscriber(id string) {
	subscriber, ok := sfu.subscribers.LoadAndDelete(id)
	if !ok || subscriber == nil {
		return
	}

	if subscriber.pc != nil {
		_ = subscriber.pc.Close()
	}

	publisher, ok := sfu.publishers.Load(subscriber.publisherKey)
	if !ok {
		return
	}

	for _, broadcaster := range publisher.GetBroadcasters() {
		broadcaster.RemoveSubscriber(subscriber.pc)
	}

	remainingCount := publisher.RemoveSubscriber(subscriber.pc)
	if remainingCount == 0 {
		sfu.cleanupPublisher(subscriber.publisherKey)
	}
}

func (sfu *LocalSFU) cleanupPublisher(publisherKey string) {
	publisher, ok := sfu.publishers.LoadAndDelete(publisherKey)
	if !ok || publisher == nil {
		return
	}
	publisher.Close()
}

func (sfu *LocalSFU) RemovePublisher(id string) {
	var keysToDelete []string
	sfu.publishers.Range(func(key string, value *Publisher) bool {
		if len(key) > len(id) && key[:len(id)] == id && key[len(id)] == '_' {
			keysToDelete = append(keysToDelete, key)
		}
		return true
	})

	for _, publisherKey := range keysToDelete {
		sfu.cleanupPublisher(publisherKey)
	}
}

func (sfu *LocalSFU) AddSubscriberICE(id string, candidate webrtc.ICECandidateInit) error {
	subscriber, ok := sfu.subscribers.Load(id)
	if !ok {
		return nil
	}
	return subscriber.pc.AddICECandidate(candidate)
}

func (sfu *LocalSFU) SetPublisherAnswer(grabberID string, streamType string, answer webrtc.SessionDescription) error {
	publisherKey := getPublisherKey(sockets.SocketID(grabberID), streamType)
	publisher, ok := sfu.publishers.Load(publisherKey)
	if !ok || publisher.pc == nil {
		return nil
	}

	parsed, err := answer.Unmarshal()
	if err == nil {
		expectedTracks := 0
		for _, media := range parsed.MediaDescriptions {
			if media.MediaName.Media == "video" || media.MediaName.Media == "audio" {
				expectedTracks++
			}
		}
		publisher.SetExpectedTracks(expectedTracks)
	}

	return publisher.pc.SetRemoteDescription(answer)
}

func (sfu *LocalSFU) AddPublisherICE(grabberID string, streamType string, candidate webrtc.ICECandidateInit) error {
	publisherKey := getPublisherKey(sockets.SocketID(grabberID), streamType)
	publisher, ok := sfu.publishers.Load(publisherKey)
	if !ok || publisher.pc == nil {
		return nil
	}
	return publisher.pc.AddICECandidate(candidate)
}
