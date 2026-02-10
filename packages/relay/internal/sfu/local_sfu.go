package sfu

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/utils"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

const (
	PublisherWaitingTime = 20 * time.Second
	BufferSize           = 1500
)

type Publisher struct {
	subscribers     map[*webrtc.PeerConnection]struct{}
	broadcasters    []*TrackBroadcaster
	pc              *webrtc.PeerConnection
	setupChan       chan struct{} // TODO: think about this field and about setupInProgress field (atomic)
	setupInProgress int32
	mu              sync.RWMutex
}

func NewPublisher() *Publisher {
	return &Publisher{
		subscribers:  make(map[*webrtc.PeerConnection]struct{}),
		broadcasters: make([]*TrackBroadcaster, 0),
		setupChan:    make(chan struct{}),
	}
}

func (p *Publisher) AddSubscriber(pc *webrtc.PeerConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribers[pc] = struct{}{}
}

func (p *Publisher) RemoveSubscriber(pc *webrtc.PeerConnection) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.subscribers, pc)

	return len(p.subscribers)
}

func (p *Publisher) GetSubscribers() []*webrtc.PeerConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	subs := make([]*webrtc.PeerConnection, 0, len(p.subscribers))
	for pc := range p.subscribers {
		subs = append(subs, pc)
	}
	return subs
}

func (p *Publisher) AddBroadcaster(broadcaster *TrackBroadcaster) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broadcasters = append(p.broadcasters, broadcaster)
}

func (p *Publisher) GetBroadcasters() []*TrackBroadcaster {
	p.mu.RLock()
	defer p.mu.RUnlock()

	broadcasters := make([]*TrackBroadcaster, len(p.broadcasters))
	copy(broadcasters, p.broadcasters)
	return broadcasters
}

func (p *Publisher) BroadcasterCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.broadcasters)
}

func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pc != nil {
		_ = p.pc.Close()
		p.pc = nil
	}

	for _, broadcaster := range p.broadcasters {
		broadcaster.Stop()
	}

	for sub := range p.subscribers {
		_ = sub.Close()
	}

	p.subscribers = nil
	p.broadcasters = nil
}

type Subscriber struct {
	pc           *webrtc.PeerConnection
	publisherKey string
}

func NewSubscriber() *Subscriber {
	return &Subscriber{}
}

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
	debug.SetGCPercent(20) // SPECIFIC THING

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
		se.SetNAT1To1IPs([]string{
			publicIP,
		}, webrtc.ICECandidateTypeHost)
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

	pm := &LocalSFU{
		api:         webrtcApi,
		config:      cfg,
		ctx:         ctx,
		cancel:      cancel,
		publishers:  utils.NewSyncMapWrapper[string, *Publisher](),
		subscribers: utils.NewSyncMapWrapper[string, *Subscriber](),
	}

	return pm, nil
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
}

func (pm *LocalSFU) DeleteSubscriber(id sockets.SocketID) {
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
		trackType := "unknown"
		if localTrack.Kind() == webrtc.RTPCodecTypeVideo {
			trackType = "video"
		} else if localTrack.Kind() == webrtc.RTPCodecTypeAudio {
			trackType = "audio"
		}
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

func (pm *LocalSFU) AddSubscriber(id sockets.SocketID, ctx *NewSubscriberContext) *api.PlayerMessage {
	setupStartTime := time.Now()
	publisherKey := getPublisherKey(ctx.publisherSocketID, ctx.streamType)

	if err := pm.validatePublisher(publisherKey, ctx); err != nil {
		slog.Warn("publisher validation failed", "error", err)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPC, err := pm.createSubscriberPeerConnection(id, ctx.streamType)
	if err != nil {
		slog.Error("failed to create subscriber peer connection", "error", err)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriber := pm.createSubscriber(subscriberPC, publisherKey, id)
	if err := pm.addTracksToSubscriber(subscriber, publisherKey, id); err != nil {
		slog.Error("failed to add tracks to subscriber", "error", err)
		pm.DeleteSubscriber(id)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	answer, err := pm.completeSignaling(subscriberPC, ctx.offer, id, ctx.streamType, ctx.c, publisherKey)
	if err != nil {
		slog.Error("failed to complete signaling", "error", err)
		pm.DeleteSubscriber(id)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	metrics.PeerConnectionSetupDuration.WithLabelValues("subscriber").Observe(time.Since(setupStartTime).Seconds())
	if publisher, ok := pm.publishers.Load(publisherKey); ok {
		metrics.SubscribersPerPublisher.Observe(float64(len(publisher.GetSubscribers())))
	}

	return &api.PlayerMessage{
		Event: api.PlayerMessageEventOfferAnswer,
		OfferAnswer: &api.OfferAnswerMessage{
			PeerId: publisherKey,
			Answer: *answer,
		},
	}
}

func (pm *LocalSFU) validatePublisher(publisherKey string, ctx *NewSubscriberContext) error {

	if !pm.ensureGrabberConnection(publisherKey, ctx.publisherSocketID, ctx.streamType, ctx.publisherConn) {
		return fmt.Errorf("could not ensure grabber connection")
	}

	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		return fmt.Errorf("no publisher found for %s", publisherKey)
	}

	trackCount := publisher.BroadcasterCount()
	if trackCount == 0 {
		return fmt.Errorf("no tracks available for %s/%s", publisherKey, ctx.streamType)
	}
	return nil
}

func (pm *LocalSFU) createSubscriberPeerConnection(id sockets.SocketID, streamType string) (*webrtc.PeerConnection, error) {
	subscriberPC, err := pm.api.NewPeerConnection(pm.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		metrics.PeerConnectionFailuresTotal.WithLabelValues("subscriber_creation_failed").Inc()
		return nil, fmt.Errorf("failed to create peer connection for %s: %w", id, err)
	}

	metrics.ActivePeerConnections.WithLabelValues("subscriber").Inc()
	metrics.PeerConnectionsCreatedTotal.WithLabelValues("subscriber").Inc()

	subscriberPeerID := fmt.Sprintf("%s_%s", id, streamType)
	pm.setupICEStateMonitoring(subscriberPC, id, subscriberPeerID, streamType)

	return subscriberPC, nil
}

func (pm *LocalSFU) setupICEStateMonitoring(pc *webrtc.PeerConnection, id sockets.SocketID,
	subscriberPeerID, streamType string) {

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Debug("ICE connection state for subscriber", "subscriberID", id, "streamType", streamType, "state", state.String())
		metrics.PeerConnectionStateChanges.WithLabelValues("subscriber", state.String()).Inc()
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			slog.Debug("player disconnected or failed, cleaning up", "playerID", id, "peerID", subscriberPeerID)
			pm.DeleteSubscriber(id)
		}
	})
}

func (pm *LocalSFU) createSubscriber(pc *webrtc.PeerConnection, publisherKey string, id sockets.SocketID) *Subscriber {
	subscriber := NewSubscriber()
	subscriber.pc = pc
	subscriber.publisherKey = publisherKey
	pm.subscribers.Store(string(id), subscriber)
	return subscriber
}

func (pm *LocalSFU) addTracksToSubscriber(subscriber *Subscriber, publisherKey string, id sockets.SocketID) error {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		return fmt.Errorf("publisher not found: %s", publisherKey)
	}
	publisher.AddSubscriber(subscriber.pc)

	trackCount := publisher.BroadcasterCount()
	slog.Debug("adding tracks to subscriber", "trackCount", trackCount, "subscriberID", id)

	tracksAdded := 0
	for _, broadcaster := range publisher.GetBroadcasters() {
		localTrack := broadcaster.GetLocalTrack()
		rtpSender, err := subscriber.pc.AddTrack(localTrack)
		if err != nil {
			return fmt.Errorf("failed to add track: %w", err)
		}
		go pm.processRTCPFeedback(rtpSender, publisher.pc, broadcaster.GetRemoteSSRC(), id)

		broadcaster.AddSubscriber(subscriber.pc)
		tracksAdded++

		trackType := "unknown"
		if localTrack.Kind() == webrtc.RTPCodecTypeVideo {
			trackType = "video"
		} else if localTrack.Kind() == webrtc.RTPCodecTypeAudio {
			trackType = "audio"
		}
		metrics.TracksAddedTotal.WithLabelValues(trackType).Inc()
		metrics.ActiveTracks.WithLabelValues(trackType).Inc()
	}

	if tracksAdded == 0 {
		return fmt.Errorf("no tracks were added")
	}
	return nil
}

func (pm *LocalSFU) processRTCPFeedback(
	sender *webrtc.RTPSender,
	publisherPC *webrtc.PeerConnection,
	remoteSSRC uint32,
	subscriberID sockets.SocketID,
) {
	rtcpBuf := make([]byte, 1500)

	for {
		n, _, err := sender.Read(rtcpBuf)
		if err != nil {
			return
		}

		packets, err := rtcp.Unmarshal(rtcpBuf[n:])
		if err != nil {
			continue
		}

		for _, pkt := range packets {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				metrics.PLIRequestsTotal.Inc()
				slog.Debug("relaying PLI to publisher", "subscriberID", subscriberID, "ssrc", remoteSSRC)
				if err := publisherPC.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{MediaSSRC: remoteSSRC},
				}); err != nil {
					return
				}
			case *rtcp.TransportLayerNack:
				metrics.NACKRequestsTotal.Inc()
			}
		}
	}
}

func (pm *LocalSFU) completeSignaling(pc *webrtc.PeerConnection, offer *webrtc.SessionDescription,
	id sockets.SocketID, streamType string, c sockets.Socket, publisherKey string) (*webrtc.SessionDescription, error) {

	subscriberPeerID := fmt.Sprintf("%s_%s", id, streamType)

	if err := pc.SetRemoteDescription(*offer); err != nil {
		return nil, fmt.Errorf("failed to set remote description for %s: %w", subscriberPeerID, err)
	}

	pm.setupICECandidateHandler(pc, c, publisherKey)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create answer for %s: %w", subscriberPeerID, err)
	}

	if err := pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("failed to set local description for %s: %w", subscriberPeerID, err)
	}

	return &answer, nil
}

func (pm *LocalSFU) setupICECandidateHandler(pc *webrtc.PeerConnection, c sockets.Socket, publisherKey string) {
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
}

func (pm *LocalSFU) ensureGrabberConnection(publisherKey string, publisherSocketID sockets.SocketID,
	streamType string, publisherConn sockets.Socket) bool {

	publisher, loaded := pm.publishers.LoadOrStore(publisherKey, NewPublisher())
	if loaded {
		if atomic.LoadInt32(&publisher.setupInProgress) > 0 {
			slog.Debug("setup in progress, waiting", "publisherKey", publisherKey)
			select {
			case <-publisher.setupChan:
				slog.Debug("setup completed", "publisherKey", publisherKey)
				return true
			case <-time.After(PublisherWaitingTime):
				slog.Warn("timeout waiting for setup", "publisherKey", publisherKey)
				return false
			}
		}
		return true
	}

	if !atomic.CompareAndSwapInt32(&publisher.setupInProgress, 0, 1) {
		select {
		case <-publisher.setupChan:
			return true
		case <-time.After(PublisherWaitingTime):
			return false
		}
	}

	go pm.setupGrabberPeerConnection(publisherSocketID, publisher, streamType, publisherConn)

	select {
	case <-publisher.setupChan:
		slog.Debug("setup completed", "publisherKey", publisherKey)
		return true
	case <-time.After(PublisherWaitingTime):
		slog.Warn("timeout during setup", "publisherKey", publisherKey)
		pm.cleanupPublisher(publisherKey)
		return false
	}
}

func (pm *LocalSFU) SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit) {
	subscriber, ok := pm.subscribers.Load(string(id))
	if !ok {
		slog.Warn("no subscriber peer connections", "subscriberID", id)
		return
	}

	if err := subscriber.pc.AddICECandidate(candidate); err != nil {
		slog.Error("failed to add ICE candidate to subscriber peer connection", "subscriberID", id, "error", err)
	}
}

func (pm *LocalSFU) DeletePublisher(id sockets.SocketID) {
	var keysToDelete []string
	pm.publishers.Range(func(key string, value *Publisher) bool {
		if len(key) > len(id) && key[:len(id)] == string(id) && key[len(id)] == '_' {
			keysToDelete = append(keysToDelete, key)
		}
		return true
	})

	for _, publisherKey := range keysToDelete {
		pm.cleanupPublisher(publisherKey)
	}
}

func (pm *LocalSFU) OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription) {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		slog.Warn("no peer connection for publisher", "publisherKey", publisherKey)
		return
	}

	if publisher.pc == nil {
		slog.Warn("publisher has no peer connection", "publisherKey", publisherKey)
		return
	}

	if err := publisher.pc.SetRemoteDescription(answer); err != nil {
		slog.Error("failed to set remote description for publisher", "publisherKey", publisherKey, "error", err)
	}
}

func (pm *LocalSFU) AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit) {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		slog.Warn("no peer connection for publisher", "publisherKey", publisherKey)
		return
	}

	if publisher.pc == nil {
		slog.Warn("publisher has no peer connection", "publisherKey", publisherKey)
		return
	}

	if err := publisher.pc.AddICECandidate(candidate); err != nil {
		slog.Error("failed to add ICE candidate to publisher", "publisherKey", publisherKey, "error", err)
	}
}

func (pm *LocalSFU) setupGrabberPeerConnection(publisherSocketID sockets.SocketID, publisher *Publisher,
	streamType string, publisherConn sockets.Socket) {
	setupStartTime := time.Now()
	publisherKey := getPublisherKey(publisherSocketID, streamType)
	slog.Info("setting up publisher peer connection", "publisherSocketID", publisherSocketID, "streamType", streamType)

	defer func() {
		if r := recover(); r != nil {
			slog.Error("CRITICAL PANIC in setupGrabberPeerConnection", "panic", r, "stack", string(debug.Stack()))
			atomic.StoreInt32(&publisher.setupInProgress, 0)
			pm.cleanupPublisher(publisherKey)
		}
	}()

	defer func() {
		atomic.StoreInt32(&publisher.setupInProgress, 0)
		close(publisher.setupChan)
	}()

	config := pm.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := pm.api.NewPeerConnection(config)
	if err != nil {
		slog.Error("failed to create publisher peer connection", "publisherSocketID", publisherSocketID, "error", err)
		metrics.PeerConnectionFailuresTotal.WithLabelValues("publisher_creation_failed").Inc()
		pm.cleanupPublisher(publisherKey)
		return
	}

	metrics.ActivePeerConnections.WithLabelValues("publisher").Inc()
	metrics.PeerConnectionsCreatedTotal.WithLabelValues("publisher").Inc()

	publisher.pc = pc

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		slog.Error("failed to add video transceiver for publisher", "publisherSocketID", publisherSocketID, "error", err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	if streamType == "webcam" {
		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if err != nil {
			slog.Error("failed to add audio transceiver for publisher", "publisherSocketID", publisherSocketID, "error", err)
			pm.cleanupPublisher(publisherKey)
			return
		}
	}

	firstTrackReceived := false
	trackTimeout := time.NewTimer(PublisherWaitingTime)
	defer trackTimeout.Stop()

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		slog.Info("track received", "trackID", remoteTrack.ID(), "kind", remoteTrack.Kind(), "codec", remoteTrack.Codec().MimeType, "payloadType", remoteTrack.Codec().PayloadType)

		if pm.config.DisableAudio && remoteTrack.Kind() == webrtc.RTPCodecTypeAudio {
			slog.Debug("track is audio but DisableAudio enabled, skipping track")
			return
		}

		broadcaster, err := NewTrackBroadcaster(remoteTrack, publisherSocketID)
		if err != nil {
			slog.Error("failed to create broadcaster for publisher", "publisherSocketID", publisherSocketID, "error", err)
			return
		}

		publisher.AddBroadcaster(broadcaster)

		subscribers := publisher.GetSubscribers()
		slog.Debug("adding new track to existing subscribers", "subscribersCount", len(subscribers), "publisherSocketID", publisherSocketID)

		for _, subscriberPC := range subscribers {
			localTrack := broadcaster.GetLocalTrack()
			rtpSender, err := subscriberPC.AddTrack(localTrack)
			if err != nil {
				slog.Error("failed to add track to existing subscriber", "error", err)
				continue
			}

			go pm.processRTCPFeedback(rtpSender, publisher.pc, broadcaster.GetRemoteSSRC(), publisherSocketID)
			broadcaster.AddSubscriber(subscriberPC)

			trackType := "unknown"
			if localTrack.Kind() == webrtc.RTPCodecTypeVideo {
				trackType = "video"
			} else if localTrack.Kind() == webrtc.RTPCodecTypeAudio {
				trackType = "audio"
			}
			metrics.TracksAddedTotal.WithLabelValues(trackType).Inc()
			metrics.ActiveTracks.WithLabelValues(trackType).Inc()
		}

		if !firstTrackReceived {
			firstTrackReceived = true
			trackTimeout.Reset(2 * time.Second)
		} else {
			trackTimeout.Reset(2 * time.Second)
		}
	})

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			metrics.ICECandidatesTotal.WithLabelValues(candidate.Typ.String()).Inc()
			if err := publisherConn.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventPlayerIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			}); err != nil {
				slog.Error("failed to send ICE candidate to publisher", "publisherSocketID", publisherSocketID, "error", err)
			}
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		slog.Debug("ICE connection state for publisher", "publisherSocketID", publisherSocketID, "state", state.String())
		metrics.PeerConnectionStateChanges.WithLabelValues("publisher", state.String()).Inc()
		if state == webrtc.ICEConnectionStateClosed || state == webrtc.ICEConnectionStateFailed {
			slog.Warn("grabber disconnected or failed, cleaning up", "publisherKey", publisherKey)
			pm.cleanupPublisher(publisherKey)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		slog.Error("failed to create offer for publisher", "publisherSocketID", publisherSocketID, "error", err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		slog.Error("failed to set local description for publisher", "publisherSocketID", publisherSocketID, "error", err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	if err := publisherConn.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventOffer,
		Offer: &api.OfferMessage{
			Offer:      offer,
			PeerId:     &publisherKey,
			StreamType: streamType,
		},
	}); err != nil {
		slog.Error("failed to send offer to publisher", "publisherSocketID", publisherSocketID, "error", err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	<-trackTimeout.C
	if !firstTrackReceived {
		slog.Warn("timeout waiting for first track from publisher", "publisherSocketID", publisherSocketID)
		pm.cleanupPublisher(publisherKey)
		return
	}

	trackCount := publisher.BroadcasterCount()
	metrics.PeerConnectionSetupDuration.WithLabelValues("publisher").Observe(time.Since(setupStartTime).Seconds())
	metrics.TracksPerPublisher.Observe(float64(trackCount))

	slog.Info("track setup completed for grabber", "publisherSocketID", publisherSocketID, "trackCount", trackCount)
}
