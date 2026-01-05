package signalling

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type PlayerConnectionLoop struct {
	socket     sockets.Socket
	socketID   sockets.SocketID
	messages   chan interface{}
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	pingTicker *time.Ticker
}

func NewPlayerConnectionLoop(socket sockets.Socket, socketID sockets.SocketID) *PlayerConnectionLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &PlayerConnectionLoop{
		socket:     socket,
		socketID:   socketID,
		messages:   make(chan interface{}, 10),
		ctx:        ctx,
		cancel:     cancel,
		pingTicker: time.NewTicker(30 * time.Second),
	}
}

func (l *PlayerConnectionLoop) Start() {
	l.wg.Add(2)
	go l.messageWriterLoop()
	go l.pingLoop()
}

func (l *PlayerConnectionLoop) Stop() {
	l.cancel()
	l.pingTicker.Stop()
	close(l.messages)
	l.wg.Wait()
}

func (l *PlayerConnectionLoop) SendMessage(msg interface{}) {
	select {
	case l.messages <- msg:
	case <-l.ctx.Done():
	}
}

func (l *PlayerConnectionLoop) messageWriterLoop() {
	defer l.wg.Done()

	for {
		select {
		case msg, ok := <-l.messages:
			if !ok {
				return
			}
			if err := l.socket.WriteJSON(msg); err != nil {
				slog.Error("failed to send message to player", "socketID", l.socketID, "error", err)
				return
			}
		case <-l.ctx.Done():
			return
		}
	}
}

func (l *PlayerConnectionLoop) pingLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.pingTicker.C:
			if err := l.socket.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventPing,
				Ping:  &api.PingMessage{Timestamp: time.Now().Unix()},
			}); err != nil {
				slog.Error("failed to send ping", "socketID", l.socketID, "error", err)
				return
			}
		case <-l.ctx.Done():
			return
		}
	}
}
