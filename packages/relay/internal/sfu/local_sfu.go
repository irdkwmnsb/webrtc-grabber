package sfu

import (
	"context"
	"fmt"
	"hash/maphash"
	"log/slog"
	"runtime"
	"runtime/debug"
	"slices"
	"sync/atomic"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sdpconv"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v4"
)

const (
	PublisherWaitingTime = 20 * time.Second
	BufferSize           = 1500
)

type Subscriber struct {
	id           sockets.SocketID
	pc           *webrtc.PeerConnection
	publisherKey string
}

func NewSubscriber(id sockets.SocketID, pc *webrtc.PeerConnection, publisherKey string) *Subscriber {
	return &Subscriber{
		id:           id,
		pc:           pc,
		publisherKey: publisherKey,
	}
}

type LocalSFU struct {
	publishers  *utils.SyncMapWrapper[string, *Publisher]
	subscribers *utils.SyncMapWrapper[string, *Subscriber]
	config      *config.WebRTCConfig
	api         *webrtc.API
	ctx         context.Context
	cancel      context.CancelFunc

	eventShards []chan Event
}

const eventShardBufferSize = 256

var eventShardSeed = maphash.MakeSeed()

func NewLocalSFU(
	parent context.Context,
	cfg *config.WebRTCConfig,
	publicIP string,
	numShards int,
) (*LocalSFU, error) {
	api, err := buildPionAPI(cfg, publicIP)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)

	return &LocalSFU{
		api:         api,
		config:      cfg,
		ctx:         ctx,
		cancel:      cancel,
		publishers:  utils.NewSyncMapWrapper[string, *Publisher](),
		subscribers: utils.NewSyncMapWrapper[string, *Subscriber](),
		eventShards: buildEventShards(numShards),
	}, nil
}

func buildPionAPI(cfg *config.WebRTCConfig, publicIP string) (*webrtc.API, error) {
	mediaEngine, err := buildMediaEngine(cfg)
	if err != nil {
		return nil, err
	}
	registry, err := buildInterceptorRegistry(mediaEngine)
	if err != nil {
		return nil, err
	}
	settingEngine, err := buildSettingEngine(cfg, publicIP)
	if err != nil {
		return nil, err
	}
	return webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(registry),
		webrtc.WithSettingEngine(settingEngine),
	), nil
}

func buildMediaEngine(cfg *config.WebRTCConfig) (*webrtc.MediaEngine, error) {
	mediaEngine := &webrtc.MediaEngine{}
	for _, codec := range cfg.Codecs {
		if err := mediaEngine.RegisterCodec(codec.Params, codec.Type); err != nil {
			return nil, fmt.Errorf("register codec: %w", err)
		}
	}
	return mediaEngine, nil
}

func buildInterceptorRegistry(mediaEngine *webrtc.MediaEngine) (*interceptor.Registry, error) {
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, fmt.Errorf("register default interceptors: %w", err)
	}
	pli, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		return nil, fmt.Errorf("create PLI factory: %w", err)
	}
	registry.Add(pli)
	return registry, nil
}

func buildSettingEngine(cfg *config.WebRTCConfig, publicIP string) (webrtc.SettingEngine, error) {
	var settingEngine webrtc.SettingEngine
	if len(cfg.PeerConnectionConfig.IceServers) == 0 && publicIP != "" {
		settingEngine.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        []string{publicIP},
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteReplace,
		})
	}
	if cfg.PortMin > 0 && cfg.PortMax > 0 {
		if err := settingEngine.SetEphemeralUDPPortRange(cfg.PortMin, cfg.PortMax); err != nil {
			return settingEngine, fmt.Errorf("set port range: %w", err)
		}
	}
	return settingEngine, nil
}

func buildEventShards(numShards int) []chan Event {
	if numShards <= 0 {
		numShards = runtime.GOMAXPROCS(0)
	}
	shards := make([]chan Event, numShards)
	for i := range shards {
		shards[i] = make(chan Event, eventShardBufferSize)
	}
	return shards
}

func (pm *LocalSFU) NumEventShards() int {
	return len(pm.eventShards)
}

func (pm *LocalSFU) EventShard(index int) <-chan Event {
	return pm.eventShards[index]
}

func (pm *LocalSFU) shardOf(event *Event) int {
	key := PublisherKey(event.PublisherID, event.StreamType)
	return int(maphash.String(eventShardSeed, key) % uint64(len(pm.eventShards)))
}

func (pm *LocalSFU) emit(event Event) {
	ch := pm.eventShards[pm.shardOf(&event)]
	if event.Kind.Critical() {
		select {
		case ch <- event:
		case <-pm.ctx.Done():
		}
		return
	}

	select {
	case ch <- event:
	default:
		// non-critical (ICE), pion should be adaptive here
	}
}

func (pm *LocalSFU) Close() {
	pm.cancel()

	pm.publishers.Range(func(key string, value *Publisher) bool {
		if value != nil {
			value.Close()
		}
		return true
	})
	pm.publishers.Clear()

	pm.subscribers.Range(func(key string, value *Subscriber) bool {
		if value != nil && value.pc != nil {
			_ = value.pc.Close()
		}
		return true
	})
	pm.subscribers.Clear()

	for _, ch := range pm.eventShards {
		close(ch)
	}
}

func (pm *LocalSFU) Unsubscribe(id sockets.SocketID) {
	subscriber, ok := pm.subscribers.LoadAndDelete(string(id))
	if !ok || subscriber == nil {
		return
	}

	if subscriber.pc != nil {
		_ = subscriber.pc.Close()
		metrics.ActivePeerConnections.WithLabelValues("subscriber").Dec()
	}

	publisher, ok := pm.publishers.Load(subscriber.publisherKey)
	if !ok {
		return
	}

	for _, broadcaster := range publisher.GetBroadcasters() {
		broadcaster.RemoveSubscriber(subscriber.pc)

		localTrack := broadcaster.GetLocalTrack()
		trackType := localTrack.Kind().String()
		metrics.ActiveTracks.WithLabelValues(trackType).Dec()
	}

	remainingCount := publisher.RemoveSubscriber(subscriber.pc)
	slog.Debug("removed subscriber from publisher", "subscriberID", id, "publisherKey", subscriber.publisherKey, "remaining", remainingCount)

	if remainingCount == 0 {
		pm.cleanupPublisher(subscriber.publisherKey)
	}
}

func (pm *LocalSFU) cleanupPublisher(publisherKey string) {
	publisher, ok := pm.publishers.LoadAndDelete(publisherKey)
	if !ok || publisher == nil {
		return
	}

	slog.Debug("cleaning up publisher", "publisherKey", publisherKey)
	if publisher.pc != nil {
		metrics.ActivePeerConnections.WithLabelValues("publisher").Dec()
	}
	publisher.Close()
}

func (pm *LocalSFU) Subscribe(
	subscriberID, publisherID sockets.SocketID,
	streamType string,
	offer api.SDP,
) (api.SDP, error) {
	setupStartTime := time.Now()

	publisher, err := pm.validatePublisher(publisherID, streamType)
	if err != nil {
		slog.Warn("publisher validation failed", "error", err)
		return api.SDP{}, err
	}

	subscriberPC, err := pm.createSubscriberPeerConnection(subscriberID, streamType)
	if err != nil {
		slog.Error("failed to create subscriber peer connection", "error", err)
		return api.SDP{}, err
	}

	subscriber := pm.createSubscriber(subscriberPC, publisher, subscriberID)
	if err := pm.addTracksToSubscriber(subscriber, publisher); err != nil {
		slog.Error("failed to add tracks to subscriber", "error", err)
		pm.Unsubscribe(subscriberID)
		return api.SDP{}, err
	}

	answer, err := pm.negotiateSubscriberAnswer(subscriber, publisher, offer)
	if err != nil {
		pm.Unsubscribe(subscriberID)
		return api.SDP{}, err
	}

	metrics.PeerConnectionSetupDuration.WithLabelValues("subscriber").Observe(time.Since(setupStartTime).Seconds())
	metrics.SubscribersPerPublisher.Observe(float64(len(publisher.GetSubscribers())))

	return answer, nil
}

func (pm *LocalSFU) negotiateSubscriberAnswer(
	subscriber *Subscriber,
	publisher *Publisher,
	offer api.SDP,
) (api.SDP, error) {
	if err := subscriber.pc.SetRemoteDescription(sdpconv.ToPionSDP(offer)); err != nil {
		return api.SDP{}, fmt.Errorf("set remote: %w", err)
	}

	subscriber.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		metrics.ICECandidatesTotal.WithLabelValues(c.Typ.String()).Inc()
		pm.emit(Event{
			Kind:         EventSubscriberICECandidate,
			PublisherID:  publisher.ID,
			StreamType:   publisher.StreamType,
			SubscriberID: subscriber.id,
			ICE:          new(sdpconv.FromPionICE(c.ToJSON())),
		})
	})

	answer, err := subscriber.pc.CreateAnswer(nil)
	if err != nil {
		return api.SDP{}, fmt.Errorf("create answer: %w", err)
	}
	if err := subscriber.pc.SetLocalDescription(answer); err != nil {
		return api.SDP{}, fmt.Errorf("set local: %w", err)
	}
	return sdpconv.FromPionSDP(answer), nil
}

func (pm *LocalSFU) validatePublisher(publisherID sockets.SocketID, streamType string) (*Publisher, error) {
	publisher, err := pm.ensureGrabberConnection(publisherID, streamType)
	if err != nil {
		return nil, err
	}
	if publisher.BroadcasterCount() == 0 {
		return nil, fmt.Errorf("no tracks available for %s", publisher.Key)
	}
	return publisher, nil
}

func (pm *LocalSFU) createSubscriberPeerConnection(id sockets.SocketID, streamType string) (*webrtc.PeerConnection, error) {
	subscriberPC, err := pm.api.NewPeerConnection(pm.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		metrics.PeerConnectionFailuresTotal.WithLabelValues("subscriber_creation_failed").Inc()
		return nil, fmt.Errorf("failed to create peer connection for %s: %w", id, err)
	}

	metrics.ActivePeerConnections.WithLabelValues("subscriber").Inc()
	metrics.PeerConnectionsCreatedTotal.WithLabelValues("subscriber").Inc()

	pm.setupICEStateMonitoring(subscriberPC, id, streamType)

	return subscriberPC, nil
}

func (pm *LocalSFU) setupICEStateMonitoring(pc *webrtc.PeerConnection, id sockets.SocketID, streamType string) {

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Debug("ICE connection state for subscriber", "subscriberID", id, "streamType", streamType, "state", state.String())
		metrics.PeerConnectionStateChanges.WithLabelValues("subscriber", state.String()).Inc()
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			slog.Debug("player disconnected or failed, cleaning up", "playerID", id)
			pm.Unsubscribe(id)
		}
	})
}

func (pm *LocalSFU) createSubscriber(pc *webrtc.PeerConnection, publisher *Publisher, id sockets.SocketID) *Subscriber {
	subscriber := NewSubscriber(id, pc, publisher.Key)
	pm.subscribers.Store(string(id), subscriber)
	return subscriber
}

func (pm *LocalSFU) addTracksToSubscriber(subscriber *Subscriber, publisher *Publisher) error {
	publisher.AddSubscriber(subscriber.pc)

	trackCount := publisher.BroadcasterCount()
	slog.Debug("adding tracks to subscriber", "trackCount", trackCount, "subscriberID", subscriber.id)

	tracksAdded := 0
	for _, broadcaster := range publisher.GetBroadcasters() {
		localTrack := broadcaster.GetLocalTrack()
		rtpSender, err := subscriber.pc.AddTrack(localTrack)
		if err != nil {
			return fmt.Errorf("failed to add track: %w", err)
		}
		go broadcaster.RelayRTCP(rtpSender, publisher.pc)

		broadcaster.AddSubscriber(subscriber.pc)
		tracksAdded++

		trackType := localTrack.Kind().String()
		metrics.TracksAddedTotal.WithLabelValues(trackType).Inc()
		metrics.ActiveTracks.WithLabelValues(trackType).Inc()
	}

	if tracksAdded == 0 {
		return fmt.Errorf("no tracks were added")
	}
	return nil
}

func (pm *LocalSFU) ensureGrabberConnection(publisherID sockets.SocketID, streamType string) (*Publisher, error) {
	publisher, loaded := pm.publishers.LoadOrStore(
		PublisherKey(publisherID, streamType),
		NewPublisher(publisherID, streamType),
	)

	if !loaded {
		go pm.setupGrabberPeerConnection(publisher)
	}

	if !publisher.WaitSetup(pm.ctx, PublisherWaitingTime) {
		if !loaded {
			pm.cleanupPublisher(publisher.Key)
		}
		return nil, fmt.Errorf("timeout waiting for publisher setup: %s", publisher.Key)
	}
	return publisher, nil
}

func (pm *LocalSFU) SubscriberICE(id sockets.SocketID, candidate api.ICECandidate) {
	subscriber, ok := pm.subscribers.Load(string(id))
	if !ok {
		slog.Warn("no subscriber peer connections", "subscriberID", id)
		return
	}

	if err := subscriber.pc.AddICECandidate(sdpconv.ToPionICE(candidate)); err != nil {
		slog.Error("failed to add ICE candidate to subscriber peer connection", "subscriberID", id, "error", err)
	}
}

func (pm *LocalSFU) DropPublisher(publisherID sockets.SocketID) {
	var keysToDelete []string
	pm.publishers.Range(func(_ string, p *Publisher) bool {
		if p.ID == publisherID {
			keysToDelete = append(keysToDelete, p.Key)
		}
		return true
	})
	for _, key := range keysToDelete {
		pm.cleanupPublisher(key)
	}
}

func (pm *LocalSFU) findPublisherPC(publisherKey string) *webrtc.PeerConnection {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok || publisher.pc == nil {
		return nil
	}
	return publisher.pc
}

func (pm *LocalSFU) PublisherAnswer(publisherKey string, answer api.SDP) {
	pc := pm.findPublisherPC(publisherKey)
	if pc == nil {
		return
	}
	if err := pc.SetRemoteDescription(sdpconv.ToPionSDP(answer)); err != nil {
		slog.Error("set remote on publisher", "publisherKey", publisherKey, "error", err)
	}
}

func (pm *LocalSFU) PublisherICE(publisherKey string, candidate api.ICECandidate) {
	pc := pm.findPublisherPC(publisherKey)
	if pc == nil {
		return
	}
	if err := pc.AddICECandidate(sdpconv.ToPionICE(candidate)); err != nil {
		slog.Error("add ICE to publisher", "publisherKey", publisherKey, "error", err)
	}
}

func (pm *LocalSFU) setupGrabberPeerConnection(publisher *Publisher) {
	setupStart := time.Now()
	slog.Info("setting up publisher peer connection", "publisherID", publisher.ID, "streamType", publisher.StreamType)

	defer pm.recoverFromPanic(publisher)
	defer publisher.FinishSetup()

	expectedTracks, err := pm.createPublisherPC(publisher)
	if err != nil {
		pm.cleanupPublisher(publisher.Key)
		return
	}

	tracksDone := pm.attachPublisherHandlers(publisher, expectedTracks)

	if err := pm.negotiatePublisherOffer(publisher); err != nil {
		pm.cleanupPublisher(publisher.Key)
		return
	}

	if !pm.waitForTracks(tracksDone, publisher) {
		pm.cleanupPublisher(publisher.Key)
		return
	}

	trackCount := publisher.BroadcasterCount()
	metrics.PeerConnectionSetupDuration.WithLabelValues("publisher").
		Observe(time.Since(setupStart).Seconds())
	metrics.TracksPerPublisher.Observe(float64(trackCount))
	slog.Info("track setup completed", "publisherID", publisher.ID, "trackCount", trackCount)
}

func (pm *LocalSFU) recoverFromPanic(publisher *Publisher) {
	if r := recover(); r != nil {
		slog.Error("CRITICAL PANIC in setupGrabberPeerConnection",
			"panic", r, "stack", string(debug.Stack()))
		publisher.FinishSetup()
		pm.cleanupPublisher(publisher.Key)
	}
}

func (pm *LocalSFU) createPublisherPC(publisher *Publisher) ([]webrtc.RTPCodecType, error) {
	cfg := pm.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := pm.api.NewPeerConnection(cfg)
	if err != nil {
		metrics.PeerConnectionFailuresTotal.WithLabelValues("publisher_creation_failed").Inc()
		return nil, fmt.Errorf("new pc: %w", err)
	}
	publisher.pc = pc
	metrics.ActivePeerConnections.WithLabelValues("publisher").Inc()
	metrics.PeerConnectionsCreatedTotal.WithLabelValues("publisher").Inc()

	closeOnError := func(format string, err error) error {
		_ = pc.Close()
		metrics.ActivePeerConnections.WithLabelValues("publisher").Dec()
		return fmt.Errorf(format, err)
	}

	expected := []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return nil, closeOnError("add video transceiver: %w", err)
	}
	if publisher.StreamType == "webcam" && !pm.config.DisableAudio {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			return nil, closeOnError("add audio transceiver: %w", err)
		}
		expected = append(expected, webrtc.RTPCodecTypeAudio)
	}

	return expected, nil
}

func (pm *LocalSFU) attachPublisherHandlers(
	publisher *Publisher,
	expectedTracks []webrtc.RTPCodecType,
) <-chan struct{} {
	tracksDone := make(chan struct{})
	var tracksReceived atomic.Int32

	publisher.pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if !slices.Contains(expectedTracks, remoteTrack.Kind()) {
			return
		}
		broadcaster, err := NewTrackBroadcaster(pm.ctx, remoteTrack, publisher.ID)
		if err != nil {
			slog.Error("create broadcaster", "publisherID", publisher.ID, "error", err)
			return
		}
		publisher.AddBroadcaster(broadcaster)
		pm.attachBroadcasterToExistingSubscribers(broadcaster, publisher)
		if int(tracksReceived.Add(1)) >= len(expectedTracks) {
			close(tracksDone)
		}
	})

	publisher.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		metrics.ICECandidatesTotal.WithLabelValues(c.Typ.String()).Inc()
		pm.emit(Event{
			Kind:        EventPublisherICECandidate,
			PublisherID: publisher.ID,
			StreamType:  publisher.StreamType,
			ICE:         new(sdpconv.FromPionICE(c.ToJSON())),
		})
	})

	publisher.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		metrics.PeerConnectionStateChanges.WithLabelValues("publisher", state.String()).Inc()
		if state == webrtc.ICEConnectionStateClosed || state == webrtc.ICEConnectionStateFailed {
			pm.cleanupPublisher(publisher.Key)
		}
	})
	return tracksDone
}

func (pm *LocalSFU) attachBroadcasterToExistingSubscribers(
	broadcaster *TrackBroadcaster, publisher *Publisher,
) {
	for _, subPC := range publisher.GetSubscribers() {
		localTrack := broadcaster.GetLocalTrack()
		rtpSender, err := subPC.AddTrack(localTrack)
		if err != nil {
			slog.Error("add track to existing subscriber", "error", err)
			continue
		}
		go broadcaster.RelayRTCP(rtpSender, publisher.pc)
		broadcaster.AddSubscriber(subPC)
		kind := localTrack.Kind().String()
		metrics.TracksAddedTotal.WithLabelValues(kind).Inc()
		metrics.ActiveTracks.WithLabelValues(kind).Inc()
	}
}

func (pm *LocalSFU) negotiatePublisherOffer(publisher *Publisher) error {
	offer, err := publisher.pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("create offer: %w", err)
	}
	if err := publisher.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("set local: %w", err)
	}
	pm.emit(Event{
		Kind:        EventPublisherOffer,
		PublisherID: publisher.ID,
		StreamType:  publisher.StreamType,
		SDP:         new(sdpconv.FromPionSDP(offer)),
	})
	return nil
}

func (pm *LocalSFU) waitForTracks(tracksDone <-chan struct{}, publisher *Publisher) bool {
	select {
	case <-tracksDone:
		return true
	case <-time.After(PublisherWaitingTime):
		slog.Warn("timeout waiting for first track", "publisherID", publisher.ID)
		return false
	case <-pm.ctx.Done():
		return false
	}
}
