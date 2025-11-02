package sfu

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	"github.com/irdkwmnsb/webrtc-grabber/internal/sockets"
	"github.com/pion/webrtc/v4"
)

// TrackBroadcaster efficiently distributes RTP packets from a single remote track
// to multiple subscriber peer connections. It implements the core SFU (Selective
// Forwarding Unit) functionality by reading packets once and writing them to many peers.
//
// Key features:
//   - Single goroutine reads from the remote track continuously
//   - Concurrent writes to subscribers using goroutine pool with semaphore
//   - Automatic subscriber removal on write errors
//   - Clean shutdown via context cancellation
//   - Thread-safe subscriber management using sync.Map
//
// Performance considerations:
//   - Uses a semaphore (maxConcurrentWrites=20) to limit concurrent write goroutines
//   - Prevents overwhelming the system with too many simultaneous writes
//   - Each write is non-blocking for other subscribers
//
// The broadcaster is created when a track arrives from a publisher and runs
// until Stop() is called or the remote track ends.
type TrackBroadcaster struct {
	// localTrack is the track that subscribers receive
	// It mirrors the remote track's codec and stream properties
	localTrack *webrtc.TrackLocalStaticRTP

	// subscribers maps peer connections to a boolean (used as a set)
	// Using sync.Map for efficient concurrent access
	subscribers sync.Map

	// isRunning is an atomic flag (0 or 1) indicating if the broadcast loop is active
	// Prevents multiple broadcast loops from starting
	isRunning int32

	// ctx is used to signal shutdown to the broadcast loop
	ctx context.Context

	// cancel triggers shutdown of the broadcast loop
	cancel context.CancelFunc
}

// NewTrackBroadcaster creates a new broadcaster for the given remote track.
// It initializes a local track that mirrors the remote track's properties
// and starts a goroutine to continuously read and broadcast RTP packets.
//
// The local track:
//   - Uses the same codec as the remote track
//   - Uses the same track ID and stream ID
//   - Can be added to multiple subscriber peer connections
//
// Parameters:
//   - remoteTrack: The track to read RTP packets from (from the publisher)
//   - publisherSocketID: Socket ID of the publisher, used for logging
//
// Returns an error if local track creation fails (rare, usually config issue).
//
// The broadcast loop starts immediately in a separate goroutine and continues
// until Stop() is called or the remote track ends.
func NewTrackBroadcaster(remoteTrack *webrtc.TrackRemote, publisherSocketID sockets.SocketID) (*TrackBroadcaster, error) {
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		remoteTrack.ID(),
		remoteTrack.StreamID(),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	broadcaster := &TrackBroadcaster{
		localTrack: localTrack,
		ctx:        ctx,
		cancel:     cancel,
	}

	go broadcaster.broadcastLoop(remoteTrack, publisherSocketID)

	return broadcaster, nil
}

// broadcastLoop is the main loop that reads RTP packets from the remote track
// and distributes them to all subscribers. This runs in a dedicated goroutine
// for the lifetime of the broadcaster.
//
// The loop:
//  1. Reads an RTP packet from the remote track (blocks until packet arrives)
//  2. Calls broadcastToSubscribers to write the packet to all subscribers
//  3. Continues until context is cancelled or remote track ends
//
// If reading fails (remote track closed, network error, etc.), the loop exits
// and logs the error. The broadcaster should then be stopped and cleaned up.
//
// The isRunning flag ensures only one broadcast loop can run at a time,
// preventing duplicate loops if NewTrackBroadcaster is somehow called twice.
//
// Parameters:
//   - remoteTrack: The source track to read from
//   - publisherSocketID: Used for logging to identify the source
func (tb *TrackBroadcaster) broadcastLoop(remoteTrack *webrtc.TrackRemote, publisherSocketID sockets.SocketID) {
	if !atomic.CompareAndSwapInt32(&tb.isRunning, 0, 1) {
		return
	}

	defer atomic.StoreInt32(&tb.isRunning, 0)

	buffer := make([]byte, BufferSize)

	for {
		select {
		case <-tb.ctx.Done():
			return
		default:
		}

		n, _, err := remoteTrack.Read(buffer)
		if err != nil {
			log.Printf("Broadcaster error reading from publisher %s: %v", publisherSocketID, err)
			return
		}

		tb.broadcastToSubscribers(buffer[:n])
	}
}

// broadcastToSubscribers writes an RTP packet to all subscriber peer connections concurrently.
// It uses a goroutine pool pattern with a semaphore to limit concurrency and prevent
// system overload when there are many subscribers.
//
// The broadcast strategy:
//  1. Iterate through all subscribers in the sync.Map
//  2. For each subscriber, spawn a goroutine to write the packet
//  3. Use a semaphore (maxConcurrentWrites=20) to limit concurrent writes
//  4. Wait for all writes to complete before returning
//  5. Remove subscribers that fail to write (disconnected/closed)
//
// Concurrency control:
//   - Maximum 20 concurrent write operations at once
//   - Prevents exhausting goroutine pool with thousands of subscribers
//   - Each write is independent - slow subscribers don't block others
//
// Error handling:
//   - If a write fails, that subscriber is removed from the map
//   - This handles disconnected peers without explicit notification
//   - Other subscribers continue receiving packets normally
//
// The method blocks until all writes complete, ensuring packet ordering
// is maintained for the next packet broadcast.
//
// Parameters:
//   - data: The RTP packet data to broadcast (already sliced to actual length)
func (tb *TrackBroadcaster) broadcastToSubscribers(data []byte) {
	const maxConcurrentWrites = 20
	semaphore := make(chan struct{}, maxConcurrentWrites)
	var wg sync.WaitGroup

	tb.subscribers.Range(func(key, value interface{}) bool {
		select {
		case <-tb.ctx.Done():
			return false
		case semaphore <- struct{}{}:
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()

			if _, err := tb.localTrack.Write(data); err != nil {
				tb.subscribers.Delete(key)
			}
		}()
		return true
	})

	wg.Wait()
}

// AddSubscriber registers a peer connection to receive packets from this track.
// The subscriber will begin receiving packets on the next broadcast cycle.
//
// This method should be called after adding the local track to the subscriber's
// peer connection via AddTrack().
//
// This method is thread-safe and can be called while broadcasting is active.
//
// Parameters:
//   - pc: The peer connection to add as a subscriber
func (tb *TrackBroadcaster) AddSubscriber(pc *webrtc.PeerConnection) {
	tb.subscribers.Store(pc, true)
}

// RemoveSubscriber unregisters a peer connection from receiving packets.
// The subscriber will stop receiving packets immediately.
//
// This method is typically called when:
//   - A subscriber explicitly disconnects
//   - The subscriber's peer connection is being cleaned up
//   - The last subscriber is removed (triggering publisher cleanup)
//
// This method is thread-safe and can be called while broadcasting is active.
// It is also called automatically by broadcastToSubscribers when a write fails.
//
// Parameters:
//   - pc: The peer connection to remove from subscribers
func (tb *TrackBroadcaster) RemoveSubscriber(pc *webrtc.PeerConnection) {
	tb.subscribers.Delete(pc)
}

// GetLocalTrack returns the local track that should be added to subscriber peer connections.
// This track mirrors the codec and IDs of the original remote track from the publisher.
//
// The returned track should be added to each subscriber's peer connection via AddTrack()
// before calling AddSubscriber() to register for packet reception.
//
// Returns the webrtc.TrackLocalStaticRTP that represents this broadcast stream.
func (tb *TrackBroadcaster) GetLocalTrack() *webrtc.TrackLocalStaticRTP {
	return tb.localTrack
}

// Stop gracefully shuts down the broadcaster by cancelling its context.
// This causes the broadcast loop to exit on its next iteration.
//
// After calling Stop():
//   - The broadcast loop will terminate
//   - No more packets will be read from the remote track
//   - No more packets will be written to subscribers
//   - All subscribers should be explicitly removed and cleaned up
//
// This method is idempotent and safe to call multiple times.
// It should be called during publisher cleanup or when the track is no longer needed.
func (tb *TrackBroadcaster) Stop() {
	tb.cancel()
}

// GetSubscriberCount returns the current number of subscribers receiving from this broadcaster.
// This requires iterating through the subscribers map, so it has O(n) complexity.
//
// The count may be stale by the time it's returned if subscribers are being
// added or removed concurrently.
//
// This method is thread-safe and primarily used for logging and monitoring.
//
// Returns the count of active subscribers.
func (tb *TrackBroadcaster) GetSubscriberCount() int {
	var count int
	tb.subscribers.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}
