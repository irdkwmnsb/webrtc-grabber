package sockets

import (
	"sync"

	"github.com/gofiber/websocket/v2"
)

type SocketID string

type Socket interface {
	Close() error
	WriteJSON(message any) error
	ReadJSON(a any) error
}

type socketImpl struct {
	ws *websocket.Conn
	mx sync.Mutex
}

func NewSocket(ws *websocket.Conn) *socketImpl {
	return &socketImpl{
		ws: ws,
	}
}

func (s *socketImpl) Close() error {
	return s.ws.Close()
}

func (s *socketImpl) WriteJSON(message any) error {
	s.mx.Lock()
	defer s.mx.Unlock()
	return s.ws.WriteJSON(message)
}

func (s *socketImpl) ReadJSON(a any) error {
	return s.ws.ReadJSON(a)
}
