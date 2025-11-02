package sockets

import (
	"sync"

	"github.com/gofiber/contrib/websocket"
)

type SocketPool struct {
	mutex   sync.Mutex
	sockets map[SocketID]Socket
}

func NewSocketPool() *SocketPool {
	return &SocketPool{
		sockets: make(map[SocketID]Socket),
	}
}

func (p *SocketPool) AddSocket(conn *websocket.Conn) {
	id := SocketID(conn.NetConn().RemoteAddr().String())
	soc := &socketImpl{ws: conn}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	if oldConn, contains := p.sockets[id]; contains {
		_ = oldConn.Close()
	}
	p.sockets[id] = soc
	// Should I init new connection
}

func (p *SocketPool) GetSocket(id SocketID) Socket {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if conn, contains := p.sockets[id]; contains {
		return conn
	}
	return nil
}

func (p *SocketPool) CloseSocket(id SocketID) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if oldConn, contains := p.sockets[id]; contains {
		_ = oldConn.Close()
	}
}

func (p *SocketPool) Close() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, conn := range p.sockets {
		_ = conn.Close()
	}
}
