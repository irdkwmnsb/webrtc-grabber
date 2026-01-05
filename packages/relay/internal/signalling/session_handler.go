package signalling

import (
	"log/slog"

	"github.com/gofiber/contrib/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type Session struct {
	Socket   sockets.Socket
	SocketID sockets.SocketID
	Cleanup  func()
}

type SessionHandler struct {
	playersSockets *sockets.SocketPool
	grabberSockets *sockets.SocketPool
}

func NewSessionHandler(playersSockets, grabberSockets *sockets.SocketPool) *SessionHandler {
	return &SessionHandler{
		playersSockets: playersSockets,
		grabberSockets: grabberSockets,
	}
}

func (h *SessionHandler) RegisterPlayerSession(conn *websocket.Conn) *Session {
	socketID := sockets.SocketID(conn.NetConn().RemoteAddr().String())
	socket := h.playersSockets.AddSocket(conn)

	metrics.ActivePlayers.Inc()
	metrics.PlayersConnectedTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()

	cleanup := func() {
		metrics.ActivePlayers.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
		h.playersSockets.CloseSocket(socketID)
	}

	slog.Info("player session started", "socketID", socketID)

	return &Session{
		Socket:   socket,
		SocketID: socketID,
		Cleanup:  cleanup,
	}
}

func (h *SessionHandler) RegisterGrabberSession(conn *websocket.Conn, peerName string) *Session {
	socketID := sockets.SocketID(conn.NetConn().RemoteAddr().String())
	socket := h.grabberSockets.AddSocket(conn)

	metrics.ActiveAgents.Inc()
	metrics.AgentsRegisteredTotal.Inc()
	metrics.ActiveWebSocketConnections.Inc()
	metrics.WebSocketConnectionsTotal.Inc()

	cleanup := func() {
		metrics.ActiveAgents.Dec()
		metrics.ActiveWebSocketConnections.Dec()
		metrics.WebSocketDisconnectionsTotal.Inc()
		h.grabberSockets.CloseSocket(socketID)
	}

	slog.Info("grabber session started", "socketID", socketID, "name", peerName)

	return &Session{
		Socket:   socket,
		SocketID: socketID,
		Cleanup:  cleanup,
	}
}
