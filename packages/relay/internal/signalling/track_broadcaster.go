package signalling

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type TrackBroadcaster struct {
	localTrack  *webrtc.TrackLocalStaticRTP
	subscribers sync.Map
	isRunning   int32
	ctx         context.Context
	cancel      context.CancelFunc
}

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

func (tb *TrackBroadcaster) AddSubscriber(pc *webrtc.PeerConnection) {
	tb.subscribers.Store(pc, true)
}

func (tb *TrackBroadcaster) RemoveSubscriber(pc *webrtc.PeerConnection) {
	tb.subscribers.Delete(pc)
}

func (tb *TrackBroadcaster) GetLocalTrack() *webrtc.TrackLocalStaticRTP {
	return tb.localTrack
}

func (tb *TrackBroadcaster) Stop() {
	tb.cancel()
}

func (tb *TrackBroadcaster) GetSubscriberCount() int {
	var count int
	tb.subscribers.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}
