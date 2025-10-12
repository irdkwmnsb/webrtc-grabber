// Package signalling manages WebRTC peer connections for a media relay server,
// implementing a publisher-subscriber pattern for real-time video and audio streaming.
//
// The package coordinates between "grabbers" (publishers) that capture and send media,
// and "players" (subscribers) that receive and play the media. It handles WebRTC
// signaling, track distribution, and connection lifecycle management.
//
// Key components:
//   - Publisher: Represents a media source broadcasting to multiple subscribers
//   - Subscriber: Represents a media consumer receiving from a publisher
//   - PeerManager: Central orchestrator managing all peer connections
//   - TrackBroadcaster: Distributes RTP packets from a track to multiple peers
//
// Thread Safety:
// All public methods are thread-safe and can be called concurrently.
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
	// PublisherWaitingTime is the maximum duration to wait for publisher setup
	// or track reception before timing out.
	PublisherWaitingTime = 20 * time.Second

	// BufferSize is the size of RTP packet buffers in bytes.
	BufferSize = 1500
)

// Publisher represents a media source (grabber) that broadcasts tracks to multiple subscribers.
// It maintains a WebRTC peer connection to the grabber and coordinates track distribution
// to all connected subscribers.
//
// Publisher setup is synchronized using atomic operations to prevent race conditions
// when multiple subscribers try to connect to the same publisher simultaneously.
type Publisher struct {
	// subscribers holds all active subscriber peer connections
	subscribers []*webrtc.PeerConnection

	// broadcasters manages track distribution to subscribers
	broadcasters []*TrackBroadcaster

	// pc is the WebRTC peer connection to the grabber
	pc *webrtc.PeerConnection

	// setupChan signals when publisher setup is complete
	setupChan chan struct{}

	// setupInProgress is an atomic flag indicating if setup is in progress
	setupInProgress int32

	// mu protects subscribers and broadcasters slices
	mu sync.RWMutex
}

// NewPublisher creates a new Publisher instance with initialized empty collections.
func NewPublisher() *Publisher {
	return &Publisher{
		subscribers:  make([]*webrtc.PeerConnection, 0),
		broadcasters: make([]*TrackBroadcaster, 0),
		setupChan:    make(chan struct{}),
	}
}

// AddSubscriber adds a subscriber peer connection to the publisher's subscriber list.
// This method is thread-safe.
func (p *Publisher) AddSubscriber(pc *webrtc.PeerConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribers = append(p.subscribers, pc)
}

// RemoveSubscriber removes a subscriber peer connection from the publisher's list.
// Returns the number of remaining subscribers after removal.
// This method is thread-safe.
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

// GetSubscribers returns a copy of the current subscribers list.
// This method is thread-safe and returns a snapshot of subscribers.
func (p *Publisher) GetSubscribers() []*webrtc.PeerConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	subs := make([]*webrtc.PeerConnection, len(p.subscribers))
	copy(subs, p.subscribers)
	return subs
}

// AddBroadcaster adds a track broadcaster to the publisher.
// Each broadcaster manages distribution of a single track to all subscribers.
// This method is thread-safe.
func (p *Publisher) AddBroadcaster(broadcaster *TrackBroadcaster) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broadcasters = append(p.broadcasters, broadcaster)
}

// GetBroadcasters returns a copy of the current broadcasters list.
// This method is thread-safe and returns a snapshot of broadcasters.
func (p *Publisher) GetBroadcasters() []*TrackBroadcaster {
	p.mu.RLock()
	defer p.mu.RUnlock()

	broadcasters := make([]*TrackBroadcaster, len(p.broadcasters))
	copy(broadcasters, p.broadcasters)
	return broadcasters
}

// BroadcasterCount returns the number of active track broadcasters.
// This method is thread-safe.
func (p *Publisher) BroadcasterCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.broadcasters)
}

// Close shuts down the publisher, stopping all broadcasters and closing all connections.
// This includes the publisher's own peer connection and all subscriber connections.
// This method is thread-safe.
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

// Subscriber represents a media consumer (player) receiving tracks from a publisher.
// It maintains a WebRTC peer connection and references its associated publisher.
type Subscriber struct {
	pc           *webrtc.PeerConnection
	publisherKey string
}

// NewSubscriber creates a new Subscriber instance.
func NewSubscriber() *Subscriber {
	return &Subscriber{}
}

// PeerManager is the central orchestrator that manages the lifecycle of all publishers
// and subscribers in the system. It handles WebRTC signaling, connection setup,
// and resource cleanup.
//
// PeerManager maintains two primary maps:
//   - publishers: Maps publisher keys (socketID_streamType) to Publisher instances
//   - subscribers: Maps subscriber socket IDs to Subscriber instances
//
// All operations are thread-safe and can be called concurrently.
type PeerManager struct {
	// publishers maps publisher keys to Publisher instances
	publishers *SyncMapWrapper[string, *Publisher]

	// subscribers maps subscriber socket IDs to Subscriber instances
	subscribers *SyncMapWrapper[string, *Subscriber]

	// config holds server configuration including codecs and connection params
	config *ServerConfig

	// api is the WebRTC API instance with configured media engine
	api *webrtc.API

	// ctx is the context for cancellation
	ctx context.Context

	// cancel cancels the context
	cancel context.CancelFunc
}

// getPublisherKey generates a unique key for a publisher based on socket ID and stream type.
// Format: "{socketID}_{streamType}"
func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

// NewPeerManager creates a new PeerManager with the given configuration.
// It initializes the WebRTC API with configured codecs and interceptors,
// including PLI (Picture Loss Indication) for video quality management.
//
// The function also tunes garbage collection for memory optimization.
//
// Returns an error if codec registration or interceptor setup fails.
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

// Close shuts down the PeerManager, closing all publisher and subscriber connections
// and cleaning up all resources. This method should be called before application shutdown.
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

// DeleteSubscriber removes a subscriber and cleans up its resources.
// If this is the last subscriber for a publisher, the publisher is also cleaned up.
//
// This method:
//  1. Closes the subscriber's peer connection
//  2. Removes the subscriber from all track broadcasters
//  3. Removes the subscriber from the publisher's list
//  4. Cleans up the publisher if no subscribers remain
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

// cleanupPublisher removes a publisher and closes all its resources.
// This includes stopping all track broadcasters and closing the publisher's peer connection.
func (pm *PeerManager) cleanupPublisher(publisherKey string) {
	publisher, ok := pm.publishers.LoadAndDelete(publisherKey)
	if !ok || publisher == nil {
		return
	}

	log.Printf("Cleaning up publisher %s", publisherKey)
	publisher.Close()
}

// AddSubscriber connects a new subscriber to a publisher for the specified stream type.
// It creates a new WebRTC peer connection, adds all publisher tracks, and handles
// the signaling exchange (offer/answer).
//
// Parameters:
//   - id: Subscriber's socket ID
//   - publisherSocketID: Publisher's socket ID
//   - streamType: Type of stream ("webcam" or "screen")
//   - c: Subscriber's socket connection for sending messages
//   - offer: WebRTC offer from the subscriber
//   - publisherConn: Publisher's socket connection for signaling
//
// Returns a PlayerMessage containing either the answer or a failure event.
//
// The method ensures the publisher connection exists (setting it up if needed),
// validates that tracks are available, creates the subscriber peer connection,
// adds all tracks, and completes the WebRTC signaling handshake.
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

// ensureGrabberConnection ensures a publisher connection exists and is ready.
// If the publisher doesn't exist, it initiates setup. If setup is already in progress,
// it waits for completion.
//
// This method uses atomic operations and channels to synchronize concurrent attempts
// to set up the same publisher, preventing duplicate setup operations.
//
// Returns true if the publisher is ready, false if setup failed or timed out.
func (pm *PeerManager) ensureGrabberConnection(publisherKey string, publisherSocketID sockets.SocketID,
	streamType string, publisherConn sockets.Socket) bool {

	publisher, loaded := pm.publishers.LoadOrStore(publisherKey, NewPublisher())
	if loaded {
		if atomic.LoadInt32(&publisher.setupInProgress) > 0 {
			log.Printf("Setup in progress for %s, waiting...", publisherKey)
			select {
			case <-publisher.setupChan:
				log.Printf("Setup completed for %s", publisherKey)
				return true
			case <-time.After(PublisherWaitingTime):
				log.Printf("Timeout waiting for setup of %s", publisherKey)
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
		log.Printf("Setup completed for %s", publisherKey)
		return true
	case <-time.After(PublisherWaitingTime):
		log.Printf("Timeout during setup of %s", publisherKey)
		pm.cleanupPublisher(publisherKey)
		return false
	}
}

// SubscriberICE adds an ICE candidate to a subscriber's peer connection.
// This is part of the WebRTC connection establishment process.
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

// DeletePublisher removes all publishers associated with the given socket ID.
// This handles cleanup when a grabber disconnects, removing all its stream types
// (e.g., both "webcam" and "screen" streams).
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

// OfferAnswerPublisher sets the answer from a publisher (grabber) on its peer connection.
// This completes the signaling exchange initiated when the publisher connection was set up.
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

// AddICECandidatePublisher adds an ICE candidate to a publisher's peer connection.
// This is part of the WebRTC connection establishment process with the grabber.
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

// setupGrabberPeerConnection establishes a WebRTC peer connection with a grabber (publisher).
// This method runs asynchronously and handles the complete setup process:
//
//  1. Creates a peer connection with appropriate transceivers (video, and audio for webcam)
//  2. Sets up track reception handlers to create broadcasters for each incoming track
//  3. Performs WebRTC signaling (create offer, exchange ICE candidates)
//  4. Waits for all expected tracks to be received
//  5. Signals completion or failure via the publisher's setupChan
//
// The method waits for the expected number of tracks based on stream type:
//   - "webcam": Configured number of tracks (typically 2: video + audio)
//   - "screen": Single video track
//
// If setup fails at any point, the publisher is cleaned up and removed.
func (pm *PeerManager) setupGrabberPeerConnection(publisherSocketID sockets.SocketID, publisher *Publisher,
	streamType string, publisherConn sockets.Socket) {
	publisherKey := getPublisherKey(publisherSocketID, streamType)
	log.Printf("Setting up publisher peer connection for %s, streamType=%s", publisherSocketID, streamType)

	defer func() {
		atomic.StoreInt32(&publisher.setupInProgress, 0)
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
	case <-time.After(PublisherWaitingTime):
		log.Printf("Timeout waiting for tracks from publisher %s", publisherSocketID)
		pm.cleanupPublisher(publisherKey)
	}
}
