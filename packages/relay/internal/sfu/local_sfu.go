// Package sfu implements a WebRTC Selective Forwarding Unit (SFU) relay server
// with publisher-subscriber architecture for real-time video and audio streaming.
//
// The package provides a complete signaling infrastructure for connecting "grabbers"
// (publishers that capture and send media) with "players" (subscribers that receive
// and play media). It handles WebRTC connection establishment, track distribution,
// and lifecycle management for both peer types.
//
// # Architecture Overview
//
// The system consists of several key components working together:
//
//   - Server: HTTP/WebSocket server managing client connections and routing
//   - PeerManager: Orchestrates all WebRTC peer connections and signaling
//   - Publisher: Represents a media source (grabber) broadcasting to subscribers
//   - Subscriber: Represents a media consumer (player) receiving from a publisher
//   - TrackBroadcaster: Efficiently distributes RTP packets from one track to many peers
//   - Storage: Tracks active peers and their metadata with health monitoring
//
// # Key Features
//
//   - Concurrent publisher setup with atomic synchronization to prevent race conditions
//   - Automatic cleanup when subscribers disconnect or publishers fail
//   - PLI (Picture Loss Indication) support for video quality recovery
//   - Flexible codec configuration via JSON
//   - IP-based admin access control
//   - Health monitoring with periodic ping/pong and stale peer cleanup
//   - Support for multiple stream types per grabber (webcam, screen share)
//   - Efficient concurrent RTP packet broadcasting with semaphore-controlled goroutines
//
// # Connection Flow
//
// Grabber (Publisher) Flow:
//  1. Connects via WebSocket to /ws/peers/:name
//  2. Receives peer connection configuration and ping interval
//  3. Receives WebRTC offer from server when first subscriber connects
//  4. Sends answer and ICE candidates back to server
//  5. Begins streaming media tracks (video/audio)
//  6. Sends periodic ping messages with connection status
//
// Player (Subscriber) Flow:
//  1. Authenticates via credential check (admin only)
//  2. Connects to /ws/player/play endpoint
//  3. Sends WebRTC offer specifying desired publisher and stream type
//  4. Receives answer from server with track setup
//  5. Exchanges ICE candidates for connection establishment
//  6. Begins receiving media streams
//
// # Thread Safety
//
// All public APIs are thread-safe and designed for concurrent access:
//   - PeerManager uses SyncMapWrapper for publishers and subscribers
//   - Publisher uses sync.RWMutex for subscriber and broadcaster lists
//   - TrackBroadcaster uses sync.Map for subscriber management
//   - Storage uses sync.Mutex for peer tracking
//   - Atomic operations ensure safe publisher setup coordination
//
// # Configuration
//
// Server behavior is controlled via ServerConfig loaded from conf/config.json:
//   - ICE server configuration for NAT traversal
//   - Supported audio/video codecs
//   - Admin IP whitelist for access control
//   - Grabber ping interval and webcam track count
//   - TLS certificate paths for secure WebSocket connections
//
// # Usage Example
//
//	config, err := signalling.LoadServerConfig()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	app := fiber.New()
//	server, err := signalling.NewServer(&config, app)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer server.Close()
//
//	server.SetupWebSockets()
//	log.Fatal(app.Listen(fmt.Sprintf(":%d", config.ServerPort)))
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
// The setupChan is closed once setup completes, allowing waiting goroutines to proceed.
type Publisher struct {
	// subscribers holds all active subscriber peer connections
	subscribers map[*webrtc.PeerConnection]struct{}

	// broadcasters manages track distribution to subscribers
	broadcasters []*TrackBroadcaster

	// pc is the WebRTC peer connection to the grabber
	pc *webrtc.PeerConnection

	// setupChan signals when publisher setup is complete
	setupChan chan struct{} // TODO: think about this field and about setupInProgress field (atomic)

	// setupInProgress is an atomic flag indicating if setup is in progress
	setupInProgress int32

	// mu protects subscribers and broadcasters slices
	mu sync.RWMutex
}

// NewPublisher creates a new Publisher instance with initialized empty collections.
func NewPublisher() *Publisher {
	return &Publisher{
		subscribers:  make(map[*webrtc.PeerConnection]struct{}),
		broadcasters: make([]*TrackBroadcaster, 0),
		setupChan:    make(chan struct{}),
	}
}

// AddSubscriber adds a subscriber peer connection to the publisher's subscriber list.
// This method is thread-safe.
func (p *Publisher) AddSubscriber(pc *webrtc.PeerConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribers[pc] = struct{}{}
}

// RemoveSubscriber removes a subscriber peer connection from the publisher's list.
// Returns the number of remaining subscribers after removal.
// This method is thread-safe.
func (p *Publisher) RemoveSubscriber(pc *webrtc.PeerConnection) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.subscribers, pc)

	return len(p.subscribers)
}

// GetSubscribers returns a copy of the current subscribers list.
// This method is thread-safe and returns a snapshot of subscribers.
func (p *Publisher) GetSubscribers() []*webrtc.PeerConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	subs := make([]*webrtc.PeerConnection, 0, len(p.subscribers))
	for pc := range p.subscribers {
		subs = append(subs, pc)
	}
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

	for sub := range p.subscribers {
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

// LocalSFU is the central orchestrator that manages the lifecycle of all publishers
// and subscribers in the system. It handles WebRTC signaling, connection setup,
// track distribution, and resource cleanup.
//
// LocalSFU maintains two primary concurrent-safe maps:
//   - publishers: Maps publisher keys (socketID_streamType) to Publisher instances
//   - subscribers: Maps subscriber socket IDs to Subscriber instances
//
// The manager ensures proper synchronization during publisher setup to prevent
// duplicate connections when multiple subscribers connect simultaneously to the
// same publisher.
//
// All public methods are thread-safe and can be called concurrently from
// multiple goroutines handling different client connections.
type LocalSFU struct {
	// publishers maps publisher keys to Publisher instances
	publishers *utils.SyncMapWrapper[string, *Publisher]

	// subscribers maps subscriber socket IDs to Subscriber instances
	subscribers *utils.SyncMapWrapper[string, *Subscriber]

	// config holds WebRTC configuration including codecs and connection params
	config *config.WebRTCConfig

	// api is the WebRTC API instance with configured media engine
	api *webrtc.API

	// ctx is the context for cancellation
	ctx context.Context

	// cancel cancels the context
	cancel context.CancelFunc
}

// getPublisherKey generates a unique key for a publisher based on socket ID and stream type.
// This key is used throughout the system to identify specific media streams.
//
// Format: "{socketID}_{streamType}"
//
// Examples:
//   - "192.168.1.5:12345_webcam"
//   - "10.0.0.2:54321_screen"
func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

// NewLocalSFU creates a new PeerManager with the given configuration.
// It initializes the WebRTC API with configured codecs and interceptors,
// including PLI (Picture Loss Indication) for video quality management.
//
// The function also tunes garbage collection (GC at 20%) for optimal performance
// in media streaming workloads where low latency is critical.
//
// Returns an error if:
//   - Codec registration fails (invalid codec configuration)
//   - Default interceptor registration fails
//   - PLI interceptor creation fails
//
// The returned PeerManager must be closed with Close() before application shutdown
// to properly clean up all resources and connections.
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

// Close shuts down the PeerManager, closing all publisher and subscriber connections
// and cleaning up all resources. This method should be called before application shutdown.
//
// The shutdown process:
//  1. Cancels the context to signal all goroutines to stop
//  2. Closes all publisher connections and stops their broadcasters
//  3. Closes all subscriber peer connections
//  4. Clears all internal maps
//
// This method is safe to call multiple times and will not panic.
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

// DeleteSubscriber removes a subscriber and cleans up its resources.
// If this is the last subscriber for a publisher, the publisher is also cleaned up.
//
// This method is automatically called when:
//   - A subscriber's ICE connection fails or disconnects
//   - A subscriber's WebSocket connection closes
//   - Explicit cleanup is needed
//
// The cleanup process:
//  1. Closes the subscriber's peer connection
//  2. Removes the subscriber from all track broadcasters
//  3. Removes the subscriber from the publisher's subscriber list
//  4. If no subscribers remain, triggers full publisher cleanup
//
// This method is thread-safe and idempotent - calling it multiple times
// with the same ID is safe.
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

// cleanupPublisher removes a publisher and closes all its resources.
// This includes stopping all track broadcasters and closing the publisher's peer connection.
//
// This method is called when:
//   - The last subscriber disconnects from a publisher
//   - A publisher's ICE connection fails
//   - A grabber disconnects
//   - Publisher setup times out or fails
//
// This method is thread-safe and idempotent.
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

// AddSubscriber connects a new subscriber to a publisher for the specified stream type.
// It creates a new WebRTC peer connection, adds all publisher tracks, and handles
// the complete signaling exchange (offer/answer/ICE).
//
// Parameters:
//   - id: Subscriber's socket ID (typically remote IP:port)
//   - publisherSocketID: Publisher's socket ID to connect to
//   - streamType: Type of stream to receive ("webcam" or "screen")
//   - c: Subscriber's socket connection for sending ICE candidates
//   - offer: WebRTC offer from the subscriber
//   - publisherConn: Publisher's socket connection for publisher signaling
//
// Returns a PlayerMessage containing either:
//   - Success: OfferAnswer event with the WebRTC answer
//   - Failure: OfferFailed event if any step fails
//
// The method ensures:
//  1. Publisher connection exists (sets it up if needed via ensureGrabberConnection)
//  2. Tracks are available from the publisher
//  3. All publisher tracks are added to the subscriber connection
//  4. ICE candidates are exchanged between subscriber and publisher
//  5. Automatic cleanup on ICE connection failure
//
// This method is thread-safe and can handle multiple concurrent subscribers
// connecting to the same or different publishers.
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

	// Record subscriber setup metrics
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

// validatePublisher ensures the publisher connection exists and has available tracks
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

// createSubscriberPeerConnection creates a new WebRTC peer connection with ICE state monitoring
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

// setupICEStateMonitoring configures ICE connection state change handler
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

// createSubscriber creates a new subscriber instance and stores it
func (pm *LocalSFU) createSubscriber(pc *webrtc.PeerConnection, publisherKey string, id sockets.SocketID) *Subscriber {
	subscriber := NewSubscriber()
	subscriber.pc = pc
	subscriber.publisherKey = publisherKey
	pm.subscribers.Store(string(id), subscriber)
	return subscriber
}

// addTracksToSubscriber adds all publisher tracks to the subscriber connection
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

// completeSignaling handles the WebRTC offer/answer exchange and ICE candidate setup
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

// setupICECandidateHandler configures ICE candidate forwarding to the subscriber
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

// ensureGrabberConnection ensures a publisher connection exists and is ready to serve subscribers.
// If the publisher doesn't exist, it initiates asynchronous setup. If setup is already in progress,
// it waits for completion.
//
// This method uses atomic compare-and-swap operations and channels to synchronize concurrent
// attempts to set up the same publisher, preventing duplicate setup operations and ensuring
// only one goroutine performs the actual setup work.
//
// The synchronization strategy:
//  1. If publisher exists and setup complete: returns immediately
//  2. If publisher exists and setup in progress: waits on setupChan
//  3. If publisher doesn't exist: creates it and claims setup responsibility
//  4. Only the goroutine that successfully sets setupInProgress=1 performs setup
//  5. Other goroutines wait on setupChan for the setup to complete
//
// Returns:
//   - true if publisher is ready to accept subscribers
//   - false if setup failed, timed out, or was cancelled
//
// Timeout: PublisherWaitingTime (20 seconds) for setup completion
//
// This method is thread-safe and handles race conditions gracefully.
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

// SubscriberICE adds an ICE candidate to a subscriber's peer connection.
// This is part of the WebRTC ICE (Interactive Connectivity Establishment)
// process for establishing the optimal network path between peers.
//
// ICE candidates represent possible network addresses (IP/port combinations)
// that the peer can use for communication. Multiple candidates are typically
// exchanged during connection establishment.
//
// This method is called when a subscriber sends an ICE candidate via WebSocket.
// It is thread-safe and can be called concurrently for different subscribers.
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

// DeletePublisher removes all publishers associated with the given socket ID.
// This handles cleanup when a grabber disconnects, removing all its stream types.
//
// For example, if a grabber with socket ID "192.168.1.5:12345" is streaming both
// webcam and screen, this will clean up both:
//   - "192.168.1.5:12345_webcam"
//   - "192.168.1.5:12345_screen"
//
// The method iterates through all publishers and matches by socket ID prefix,
// ensuring complete cleanup of all streams from the disconnected grabber.
//
// This method is thread-safe and called automatically when a grabber's
// WebSocket connection closes.
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

// OfferAnswerPublisher sets the answer from a publisher (grabber) on its peer connection.
// This completes the offer/answer exchange that was initiated when the publisher
// connection was set up in response to the first subscriber.
//
// The WebRTC signaling flow:
//  1. Server creates offer and sends to grabber (via setupGrabberPeerConnection)
//  2. Grabber processes offer and creates answer
//  3. Grabber sends answer back to server
//  4. This method sets the answer on the publisher's peer connection
//  5. ICE candidate exchange continues to establish the connection
//
// This method is called when a grabber sends an OfferAnswer message via WebSocket.
// It is thread-safe and validates that the publisher and its peer connection exist.
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

// AddICECandidatePublisher adds an ICE candidate to a publisher's peer connection.
// This is part of the WebRTC ICE process for establishing the network connection
// between the server and the grabber.
//
// ICE candidates from the grabber are sent to the server via WebSocket and
// added to the publisher's peer connection to help establish the optimal
// network path for media transmission.
//
// This method is thread-safe and validates that the publisher and its peer
// connection exist before attempting to add the candidate.
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

// setupGrabberPeerConnection establishes a WebRTC peer connection with a grabber (publisher).
// This method runs asynchronously in its own goroutine and handles the complete setup process.
//
// The setup process:
//  1. Creates a peer connection with appropriate transceivers based on stream type
//  2. Sets up track reception handlers to create broadcasters for each incoming track
//  3. Configures ICE candidate and connection state handlers
//  4. Creates and sends WebRTC offer to the grabber
//  5. Waits for all expected tracks to be received
//  6. Signals completion or failure via the publisher's setupChan
//
// Stream type configuration:
//   - "webcam": Video + Audio transceivers
//   - "screen": Video transceiver only, expects 1 track
//
// The method waits up to PublisherWaitingTime for all expected tracks to arrive.
// If setup fails at any point, the publisher is cleaned up and removed.
//
// Track handling:
// When a track arrives via OnTrack callback, a TrackBroadcaster is created to
// distribute the track's RTP packets to all current and future subscribers.
//
// Connection monitoring:
// The ICE connection state is monitored, and the publisher is cleaned up if
// the connection fails or disconnects.
//
// This method must only be called by the goroutine that successfully claimed
// setup responsibility via atomic.CompareAndSwapInt32. The setupChan is closed
// upon completion (success or failure) to wake up any waiting goroutines.
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

	// Record setup metrics
	trackCount := publisher.BroadcasterCount()
	metrics.PeerConnectionSetupDuration.WithLabelValues("publisher").Observe(time.Since(setupStartTime).Seconds())
	metrics.TracksPerPublisher.Observe(float64(trackCount))

	slog.Info("track setup completed for grabber", "publisherSocketID", publisherSocketID, "trackCount", trackCount)
}
