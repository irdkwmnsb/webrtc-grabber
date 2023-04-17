package sockets

import (
	"github.com/fasthttp/websocket"
	"sync"
)

type fSocketImpl struct {
	ws *websocket.Conn
	mx sync.Mutex
}

func NewFastHttpSocket(ws *websocket.Conn) Socket {
	return &fSocketImpl{ws: ws}
}

func (s *fSocketImpl) Close() error {
	return s.ws.Close()
}

func (s *fSocketImpl) WriteJSON(message any) error {
	s.mx.Lock()
	defer s.mx.Unlock()
	return s.ws.WriteJSON(message)
}

func (s *fSocketImpl) ReadJSON(message any) error {
	s.mx.Lock()
	defer s.mx.Unlock()
	return s.ws.ReadJSON(message)
}
