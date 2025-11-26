package sfu

import (
	"context"
	"errors"
	"io"
	"log"
	"sync/atomic"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

const (
	rtpBufferSize   = 1500
	packetQueueSize = 100
)

type TrackBroadcaster struct {
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteSSRC      uint32
	subscriberCount int32

	ctx    context.Context
	cancel context.CancelFunc

	packetChan chan *rtp.Packet
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
		packetChan: make(chan *rtp.Packet, packetQueueSize),
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

		pkt, _, err := remoteTrack.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Printf("Publisher %s closed track", publisherSocketID)
			} else {
				log.Printf("Error reading from publisher %s: %v", publisherSocketID, err)
			}
			return
		}

		metrics.SFUPacketsReceived.Inc()
		metrics.SFUBytesReceived.Add(float64(len(pkt.Payload)))

		select {
		case tb.packetChan <- pkt:
		default:
		}
	}
}

func (tb *TrackBroadcaster) writeLoop() {
	for {
		select {
		case <-tb.ctx.Done():
			return
		case pkt := <-tb.packetChan:
			if err := tb.localTrack.WriteRTP(pkt); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					return
				}
				log.Printf("Error writing to local track: %v", err)
			} else {
				metrics.SFUPacketsSent.Inc()
				metrics.SFUBytesSent.Add(float64(len(pkt.Payload)))
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
