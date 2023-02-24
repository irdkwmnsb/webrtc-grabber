package sockets

import (
	"github.com/gofiber/websocket/v2"
	"sync"
)

type SocketID string

//type

type Socket struct {
	ID SocketID
	ws *websocket.Conn
	//handler
}

type SocketPool struct {
	mutex   sync.Mutex
	sockets map[SocketID]*websocket.Conn
}

func NewSocketPool() *SocketPool {
	return &SocketPool{
		sockets: make(map[SocketID]*websocket.Conn),
	}
}

func (p *SocketPool) AddSocket(conn *websocket.Conn) {
	id := SocketID(conn.NetConn().RemoteAddr().String())

	p.mutex.Lock()
	defer p.mutex.Unlock()
	if oldConn, contains := p.sockets[id]; contains {
		_ = oldConn.Close()
	}
	p.sockets[id] = conn
	// Should I init new connection
}

func (p *SocketPool) GetSocket(id SocketID) *websocket.Conn {
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
