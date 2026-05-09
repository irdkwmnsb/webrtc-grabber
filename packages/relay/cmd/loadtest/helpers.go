package main

import (
	"context"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/pion/webrtc/v4"

	relayapi "github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
)

type wsWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newWSWriter(c *websocket.Conn) *wsWriter {
	return &wsWriter{conn: c}
}

func (w *wsWriter) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(v)
}

func sendPing(ws *wsWriter, streamType string) error {
	return ws.writeJSON(relayapi.GrabberMessage{
		Event: relayapi.GrabberMessageEventPing,
		Ping: &relayapi.PeerStatus{
			StreamTypes: []relayapi.StreamType{relayapi.StreamType(streamType)},
		},
	})
}

func pingLoop(ctx context.Context, ws *wsWriter, streamType string) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := sendPing(ws, streamType); err != nil {
				return
			}
		}
	}
}

func buildPionAPI() *webrtc.API {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		panic(err)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me))
}
