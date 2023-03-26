package sockets

import (
	"github.com/gofiber/websocket/v2"
	"sync"
)

type SocketID string

type Socket interface {
	Close() error
	WriteJSON(message any) error
}

type socketImpl struct {
	ws *websocket.Conn
	mx sync.Mutex
}

func (s *socketImpl) Close() error {
	return s.ws.Close()
}

func (s *socketImpl) WriteJSON(message any) error {
	s.mx.Lock()
	defer s.mx.Unlock()
	return s.ws.WriteJSON(message)
}
