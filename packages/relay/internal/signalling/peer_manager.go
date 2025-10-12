package signalling

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v4"
)

const (
	PublisherWatingTime = 20 * time.Second
	BufferSize          = 1500
)

type Publisher struct {
	pc              *webrtc.PeerConnection
	mu              sync.RWMutex
	subscribers     []*webrtc.PeerConnection
	streamType      string
	broadcasters    []*TrackBroadcaster
	setupInProgress atomic.Bool
	setupChan       chan struct{}
}

func NewPublisher() *Publisher {
	return &Publisher{
		subscribers:  make([]*webrtc.PeerConnection, 0),
		broadcasters: make([]*TrackBroadcaster, 0),
		setupChan:    make(chan struct{}),
	}
}

func (p *Publisher) AddSubscriber(pc *webrtc.PeerConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribers = append(p.subscribers, pc)
}

func (p *Publisher) RemoveSubscriber(pc *webrtc.PeerConnection) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	newSubscribers := make([]*webrtc.PeerConnection, 0, len(p.subscribers))
	for _, sub := range p.subscribers {
		if sub != pc {
			newSubscribers = append(newSubscribers, sub)
		}
	}
	p.subscribers = newSubscribers
	return len(p.subscribers)
}

func (p *Publisher) GetSubscribers() []*webrtc.PeerConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	subs := make([]*webrtc.PeerConnection, len(p.subscribers))
	copy(subs, p.subscribers)
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

	for _, sub := range p.subscribers {
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

type PeerManager struct {
	publishers  *SyncMapWrapper[string, *Publisher]
	subscribers *SyncMapWrapper[string, *Subscriber]
	config      *ServerConfig

	api *webrtc.API

	ctx    context.Context
	cancel context.CancelFunc
}

func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

func NewPeerManager(config *ServerConfig) (*PeerManager, error) {
	debug.SetGCPercent(20) // SPECIFIC THING

	mediaEngine := &webrtc.MediaEngine{}
	for _, codec := range config.Codecs {
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

	webrtcApi := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	ctx, cancel := context.WithCancel(context.Background())

	pm := &PeerManager{
		api:         webrtcApi,
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
		publishers:  NewSyncMapWrapper[string, *Publisher](),
		subscribers: NewSyncMapWrapper[string, *Subscriber](),
	}

	return pm, nil
}

func (pm *PeerManager) Close() {
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

func (pm *PeerManager) DeleteSubscriber(id sockets.SocketID) {
	subscriber, ok := pm.subscribers.LoadAndDelete(string(id))
	if !ok || subscriber == nil {
		return
	}

	if subscriber.pc != nil {
		_ = subscriber.pc.Close()
	}

	publisher, ok := pm.publishers.Load(subscriber.publisherKey)
	if !ok {
		return
	}

	for _, broadcaster := range publisher.GetBroadcasters() {
		broadcaster.RemoveSubscriber(subscriber.pc)
	}

	remainingCount := publisher.RemoveSubscriber(subscriber.pc)
	log.Printf("Removed subscriber %s from publisher %s. Remaining subscribers: %d",
		id, subscriber.publisherKey, remainingCount)

	if remainingCount == 0 {
		pm.cleanupPublisher(subscriber.publisherKey)
	}
}

func (pm *PeerManager) cleanupPublisher(publisherKey string) {
	publisher, ok := pm.publishers.LoadAndDelete(publisherKey)
	if !ok || publisher == nil {
		return
	}

	log.Printf("Cleaning up publisher %s", publisherKey)
	publisher.Close()
}

func (pm *PeerManager) AddSubscriber(id, publisherSocketID sockets.SocketID,
	streamType string, c sockets.Socket,
	offer *webrtc.SessionDescription, publisherConn sockets.Socket) *api.PlayerMessage {

	publisherKey := getPublisherKey(publisherSocketID, streamType)
	if !pm.ensureGrabberConnection(publisherKey, publisherSocketID, streamType, publisherConn) {
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		log.Printf("No publisher found for %s", publisherKey)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	trackCount := publisher.BroadcasterCount()
	if trackCount == 0 {
		log.Printf("no tracks available for %s/%s", publisherKey, streamType)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPC, err := pm.api.NewPeerConnection(pm.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		log.Printf("failed to create subscriber peer connection %s: %v", id, err)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPeerID := fmt.Sprintf("%s_%s", id, streamType)

	subscriberPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for subscriber %s stream %s: %s", id, streamType, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Player %s (%s) disconnected or failed, cleaning up", id, subscriberPeerID)
			pm.DeleteSubscriber(id)
		}
	})

	subscriber := NewSubscriber()
	subscriber.pc = subscriberPC
	subscriber.publisherKey = publisherKey
	pm.subscribers.Store(string(id), subscriber)

	log.Printf("Adding %d tracks to subscriber %s (%s)", trackCount, id, subscriberPeerID)

	tracksAdded := 0
	for _, broadcaster := range publisher.GetBroadcasters() {
		if _, err := subscriberPC.AddTrack(broadcaster.GetLocalTrack()); err != nil {
			log.Printf("Failed to add track to subscriber %s: %v", id, err)
			break
		}
		broadcaster.AddSubscriber(subscriberPC)
		tracksAdded++
	}

	if tracksAdded == 0 {
		pm.DeleteSubscriber(id)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	if err := subscriberPC.SetRemoteDescription(*offer); err != nil {
		log.Printf("failed to set remote description for subscriber %s (%s): %v", id, subscriberPeerID, err)
		_ = subscriberPC.Close()
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPC.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = c.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventGrabberIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			})
		}
	})

	answer, err := subscriberPC.CreateAnswer(nil)
	if err != nil {
		log.Printf("failed to create answer for subscriber %s (%s): %v", id, subscriberPeerID, err)
		pm.DeleteSubscriber(id)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	if err := subscriberPC.SetLocalDescription(answer); err != nil {
		log.Printf("failed to set local description for subscriber %s (%s): %v", id, subscriberPeerID, err)
		pm.DeleteSubscriber(id)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	return &api.PlayerMessage{
		Event: api.PlayerMessageEventOfferAnswer,
		OfferAnswer: &api.OfferAnswerMessage{
			PeerId: publisherKey,
			Answer: answer,
		},
	}
}

func (pm *PeerManager) ensureGrabberConnection(publisherKey string, publisherSocketID sockets.SocketID,
	streamType string, publisherConn sockets.Socket) bool {

	publisher, loaded := pm.publishers.LoadOrStore(publisherKey, NewPublisher())
	if loaded {
		if publisher.setupInProgress.Load() {
			log.Printf("Setup in progress for %s, waiting...", publisherKey)
			select {
			case <-publisher.setupChan:
				log.Printf("Setup completed for %s", publisherKey)
				return true
			case <-time.After(PublisherWatingTime):
				log.Printf("Timeout waiting for setup of %s", publisherKey)
				return false
			}
		}
		return true
	}

	if !publisher.setupInProgress.CompareAndSwap(false, true) {
		select {
		case <-publisher.setupChan:
			return true
		case <-time.After(PublisherWatingTime):
			return false
		}
	}

	publisher.streamType = streamType
	go pm.setupGrabberPeerConnection(publisherSocketID, publisher, streamType, publisherConn)

	select {
	case <-publisher.setupChan:
		log.Printf("Setup completed for %s", publisherKey)
		return true
	case <-time.After(PublisherWatingTime):
		log.Printf("Timeout during setup of %s", publisherKey)
		pm.cleanupPublisher(publisherKey)
		return false
	}
}

func (pm *PeerManager) SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit) {
	subscriber, ok := pm.subscribers.Load(string(id))
	if !ok {
		log.Printf("no subscriber peer connections for %v", id)
		return
	}

	if err := subscriber.pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to subscriber %v peer connection: %v", id, err)
	}
}

func (pm *PeerManager) DeletePublisher(id sockets.SocketID) {
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

func (pm *PeerManager) OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription) {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if publisher.pc == nil {
		log.Printf("Publisher %s has no peer connection", publisherKey)
		return
	}

	if err := publisher.pc.SetRemoteDescription(answer); err != nil {
		log.Printf("failed to set remote description for publisher %s: %v", publisherKey, err)
	}
}

func (pm *PeerManager) AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit) {
	publisher, ok := pm.publishers.Load(publisherKey)
	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if publisher.pc == nil {
		log.Printf("Publisher %s has no peer connection", publisherKey)
		return
	}

	if err := publisher.pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to publisher %s: %v", publisherKey, err)
	}
}

func getTrackKey(publisherKey, trackID string) string {
	return fmt.Sprintf("%s_track_%s", publisherKey, trackID)
}

func (pm *PeerManager) setupGrabberPeerConnection(publisherSocketID sockets.SocketID, publisher *Publisher,
	streamType string, publisherConn sockets.Socket) {
	publisherKey := getPublisherKey(publisherSocketID, streamType)
	log.Printf("Setting up publisher peer connection for %s, streamType=%s", publisherSocketID, streamType)

	defer func() {
		publisher.setupInProgress.Store(false)
		close(publisher.setupChan)
	}()

	config := pm.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := pm.api.NewPeerConnection(config)
	if err != nil {
		log.Printf("failed to create publisher peer connection %s: %v", publisherSocketID, err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	publisher.pc = pc

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add video transceiver for publisher %s: %v", publisherSocketID, err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	expectedTracks := 1
	if streamType == "webcam" {
		expectedTracks = pm.config.WebcamTrackCount

		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if err != nil {
			log.Printf("failed to add audio transceiver for publisher %s: %v", publisherSocketID, err)
			pm.cleanupPublisher(publisherKey)
			return
		}
	}

	var tracksReceived atomic.Int32
	trackSetupDone := make(chan struct{})
	var trackSetupOnce sync.Once

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Track received: ID=%s, Kind=%s, Codec=%s, PayloadType=%d",
			remoteTrack.ID(), remoteTrack.Kind(), remoteTrack.Codec().MimeType, remoteTrack.Codec().PayloadType)

		broadcaster, err := NewTrackBroadcaster(remoteTrack, publisherSocketID)
		if err != nil {
			log.Printf("Failed to create broadcaster for publisher %s: %v", publisherSocketID, err)
			return
		}

		publisher.AddBroadcaster(broadcaster)
		currentCount := int(tracksReceived.Add(1))

		log.Printf("Tracks received for %s: %d/%d", publisherSocketID, currentCount, expectedTracks)

		if currentCount >= expectedTracks {
			trackSetupOnce.Do(func() {
				close(trackSetupDone)
			})
		}
	})

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			if err := publisherConn.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventPlayerIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			}); err != nil {
				log.Printf("failed to send ICE candidate to publisher %s: %v", publisherSocketID, err)
			}
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for publisher %s: %s", publisherSocketID, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Grabber %s disconnected or failed, cleaning up", publisherKey)
			pm.cleanupPublisher(publisherKey)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("failed to create offer for publisher %s: %v", publisherSocketID, err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("failed to set local description for publisher %s: %v", publisherSocketID, err)
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
		log.Printf("failed to send offer to publisher %s: %v", publisherSocketID, err)
		pm.cleanupPublisher(publisherKey)
		return
	}

	select {
	case <-trackSetupDone:
		log.Printf("All expected tracks received for grabber %s", publisherSocketID)
	case <-time.After(PublisherWatingTime):
		log.Printf("Timeout waiting for tracks from publisher %s", publisherSocketID)
		pm.cleanupPublisher(publisherKey)
	}
}
