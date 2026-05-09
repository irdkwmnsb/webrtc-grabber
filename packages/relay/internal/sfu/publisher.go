package sfu

import (
	"context"
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type Publisher struct {
	ID         sockets.SocketID
	StreamType string
	Key        string

	subscribers  map[*webrtc.PeerConnection]struct{}
	broadcasters []*TrackBroadcaster
	pc           *webrtc.PeerConnection

	setupChan chan struct{}
	setupOnce sync.Once

	mu sync.RWMutex
}

func NewPublisher(id sockets.SocketID, streamType string) *Publisher {
	return &Publisher{
		ID:           id,
		StreamType:   streamType,
		Key:          PublisherKey(id, streamType),
		subscribers:  make(map[*webrtc.PeerConnection]struct{}),
		broadcasters: make([]*TrackBroadcaster, 0),
		setupChan:    make(chan struct{}),
	}
}

func (p *Publisher) FinishSetup() {
	p.setupOnce.Do(func() { close(p.setupChan) })
}

func (p *Publisher) WaitSetup(ctx context.Context, timeout time.Duration) bool {
	select {
	case <-p.setupChan:
		return true
	case <-time.After(timeout):
		return false
	case <-ctx.Done():
		return false
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
