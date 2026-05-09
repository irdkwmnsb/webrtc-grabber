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
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

const (
	rtpBufferSize   = 1500
	packetQueueSize = 100
)

var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, rtpBufferSize)
		return &b
	},
}

type rtpPacket struct {
	buf *[]byte
	n   int
}

type TrackBroadcaster struct {
	localTrack      *webrtc.TrackLocalStaticRTP
	remoteSSRC      uint32
	subscriberCount int32

	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once // TODO: Think about this (for now i want to fix double dec)

	packetChan chan rtpPacket
}

func NewTrackBroadcaster(
	parent context.Context,
	remoteTrack *webrtc.TrackRemote,
	publisherSocketID sockets.SocketID,
) (*TrackBroadcaster, error) {
	localTrack, err := webrtc.NewTrackLocalStaticRTP(
		remoteTrack.Codec().RTPCodecCapability,
		remoteTrack.ID(),
		remoteTrack.StreamID(),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)

	broadcaster := &TrackBroadcaster{
		localTrack: localTrack,
		remoteSSRC: uint32(remoteTrack.SSRC()),
		ctx:        ctx,
		cancel:     cancel,
		packetChan: make(chan rtpPacket, packetQueueSize),
	}

	go broadcaster.readLoop(remoteTrack, publisherSocketID)
	go broadcaster.writeLoop()

	metrics.ActiveBroadcasters.Inc()

	return broadcaster, nil
}

func (tb *TrackBroadcaster) readLoop(remoteTrack *webrtc.TrackRemote, publisherSocketID sockets.SocketID) {
	defer tb.Stop()
	defer tb.drainPacketChan()

	for {
		select {
		case <-tb.ctx.Done():
			return
		default:
		}

		bufp := bufferPool.Get().(*[]byte)
		buf := (*bufp)[:cap(*bufp)]

		n, _, err := remoteTrack.Read(buf)
		if err != nil {
			bufferPool.Put(bufp)
			if errors.Is(err, io.EOF) {
				slog.Debug("publisher closed track", "publisherSocketID", publisherSocketID)
			} else {
				slog.Error("error reading from publisher", "publisherSocketID", publisherSocketID, "error", err)
			}
			return
		}

		metrics.SFUPacketsReceived.Inc()
		metrics.SFUBytesReceived.Add(float64(n))
		metrics.RTPPacketsTotal.WithLabelValues("received").Inc()
		metrics.RTPBytesTotal.WithLabelValues("received").Add(float64(n))

		select {
		case tb.packetChan <- rtpPacket{buf: bufp, n: n}:
		default:
			bufferPool.Put(bufp)
		}
	}
}

func (tb *TrackBroadcaster) writeLoop() {
	for {
		select {
		case <-tb.ctx.Done():
			return
		case pkt, ok := <-tb.packetChan:
			if !ok {
				return
			}
			data := (*pkt.buf)[:pkt.n]
			if _, err := tb.localTrack.Write(data); err != nil {
				bufferPool.Put(pkt.buf)
				if errors.Is(err, io.ErrClosedPipe) {
					return
				}
				slog.Error("error writing to local track", "error", err)
			} else {
				metrics.SFUPacketsSent.Inc()
				metrics.SFUBytesSent.Add(float64(pkt.n))
				metrics.RTPPacketsTotal.WithLabelValues("forwarded").Inc()
				metrics.RTPBytesTotal.WithLabelValues("forwarded").Add(float64(pkt.n))
				bufferPool.Put(pkt.buf)
			}
		}
	}
}

func (tb *TrackBroadcaster) RelayRTCP(sender *webrtc.RTPSender, publisherPC *webrtc.PeerConnection) {
	rtcpBuf := make([]byte, BufferSize)
	for {
		n, _, err := sender.Read(rtcpBuf)
		if err != nil {
			return
		}
		packets, err := rtcp.Unmarshal(rtcpBuf[:n])
		if err != nil {
			continue
		}
		for _, pkt := range packets {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				metrics.PLIRequestsTotal.Inc()
				if err := publisherPC.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{MediaSSRC: tb.remoteSSRC},
				}); err != nil {
					return
				}
			case *rtcp.TransportLayerNack:
				metrics.NACKRequestsTotal.Inc()
			}
		}
	}
}

func (tb *TrackBroadcaster) drainPacketChan() {
	close(tb.packetChan)
	for pkt := range tb.packetChan {
		bufferPool.Put(pkt.buf)
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
	tb.stopOnce.Do(func() {
		tb.cancel()
		metrics.ActiveBroadcasters.Dec()
	})
}

func (tb *TrackBroadcaster) GetSubscriberCount() int {
	return int(atomic.LoadInt32(&tb.subscriberCount))
}

func (tb *TrackBroadcaster) GetRemoteSSRC() uint32 {
	return tb.remoteSSRC
}
