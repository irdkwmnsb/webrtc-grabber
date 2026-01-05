package sfu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/webrtc/v4"
)

const (
	rtpBufferSize   = 1500
	packetQueueSize = 100
)

var bufferPool = sync.Pool {
	New: func() any {
		return make([]byte, rtpBufferSize)
	},
}

type TrackBroadcaster struct {
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteSSRC      uint32
	subscriberCount int32

	ctx    context.Context
	cancel context.CancelFunc

	packetChan chan []byte
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
		remoteSSRC: uint32(remoteTrack.SSRC()),
		ctx:        ctx,
		cancel:     cancel,
		packetChan: make(chan []byte, packetQueueSize),
	}

	go broadcaster.readLoop(remoteTrack, publisherSocketID)
	go broadcaster.writeLoop()

	return broadcaster, nil
}

func (tb *TrackBroadcaster) readLoop(remoteTrack *webrtc.TrackRemote, publisherSocketID sockets.SocketID) {
	defer tb.Stop()

	for {
		select {
		case <-tb.ctx.Done():
			return
		default:
		}

		buf := bufferPool.Get().([]byte)
		buf = buf[:cap(buf)]

		n, _, err := remoteTrack.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				slog.Debug("publisher closed track", "publisherSocketID", publisherSocketID)
			} else {
				slog.Error("error reading from publisher", "publisherSocketID", publisherSocketID, "error", err)
			}
			return
		}

		metrics.SFUPacketsReceived.Inc()
		metrics.SFUBytesReceived.Add(float64(n))

		select {
		case tb.packetChan <- buf[:n]:
		default:
			bufferPool.Put(buf)
		}
	}
}

func (tb *TrackBroadcaster) writeLoop() {
	for {
		select {
		case <-tb.ctx.Done():
			return
		case pkt := <-tb.packetChan:
			if _, err := tb.localTrack.Write(pkt); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					return
				}
				slog.Error("error writing to local track", "error", err)
			} else {
				metrics.SFUPacketsSent.Inc()
				metrics.SFUBytesSent.Add(float64(len(pkt)))
			}
		}
	}
}

func (tb *TrackBroadcaster) AddSubscriber(pc *webrtc.PeerConnection) {
	atomic.AddInt32(&tb.subscriberCount, 1)
}

func (tb *TrackBroadcaster) RemoveSubscriber(pc *webrtc.PeerConnection) {
	atomic.AddInt32(&tb.subscriberCount, -1)
}

func (tb *TrackBroadcaster) GetLocalTrack() *webrtc.TrackLocalStaticRTP {
	return tb.localTrack
}

func (tb *TrackBroadcaster) Stop() {
	tb.cancel()
}

func (tb *TrackBroadcaster) GetSubscriberCount() int {
	return int(atomic.LoadInt32(&tb.subscriberCount))
}

func (tb *TrackBroadcaster) GetRemoteSSRC() uint32 {
	return tb.remoteSSRC
}
