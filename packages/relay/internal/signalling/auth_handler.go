package signalling

import (
	"log/slog"
	"net/netip"

	"github.com/gofiber/contrib/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type AuthHandler struct {
	config *config.AppConfig
}

func NewAuthHandler(cfg *config.AppConfig) *AuthHandler {
	return &AuthHandler{config: cfg}
}

func (h *AuthHandler) CheckPlayerCredential(credentials string) bool {
	return h.config.Security.PlayerCredential == nil || *h.config.Security.PlayerCredential == credentials
}

func (h *AuthHandler) IsAdminIP(addrPort string) bool {
	ip, err := netip.ParseAddrPort(addrPort)
	if err != nil {
		slog.Error("failed to parse IP address", "addr", addrPort, "error", err)
		return false
	}

	for _, n := range h.config.Security.AdminsRawNetworks {
		if n.Contains(ip.Addr()) {
			return true
		}
	}
	return false
}

func (h *AuthHandler) AuthenticatePlayer(c *websocket.Conn) bool {
	socketID := sockets.SocketID(c.NetConn().RemoteAddr().String())

	if !h.IsAdminIP(string(socketID)) {
		slog.Warn("IP not in whitelist", "socketID", socketID)
		accessMessage := "Forbidden. IP address black listed"
		_ = c.WriteJSON(api.PlayerMessage{
			Event:         api.PlayerMessageEventAuthFailed,
			AccessMessage: &accessMessage,
		})
		return false
	}

	if err := c.WriteJSON(api.PlayerMessage{Event: api.PlayerMessageEventAuthRequest}); err != nil {
		return false
	}

	var message api.PlayerMessage
	if err := c.ReadJSON(&message); err != nil {
		slog.Debug("disconnected during auth", "socketID", socketID)
		return false
	}

	if message.Event != api.PlayerMessageEventAuth || message.PlayerAuth == nil || !h.CheckPlayerCredential(message.PlayerAuth.Credential) {
		accessMessage := "Forbidden. Incorrect credential"
		_ = c.WriteJSON(api.PlayerMessage{
			Event:         api.PlayerMessageEventAuthFailed,
			AccessMessage: &accessMessage,
		})
		slog.Warn("authentication failed", "socketID", socketID)
		return false
	}

	if err := c.WriteJSON(api.PlayerMessage{
		Event:    api.PlayerMessageEventInitPeer,
		InitPeer: &api.PcConfigMessage{PcConfig: h.config.WebRTC.PeerConnectionConfig},
	}); err != nil {
		slog.Error("failed to send init_peer", "socketID", socketID)
		return false
	}

	return true
}
