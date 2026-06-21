package signalling

import (
	"crypto/subtle"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
)

const (
	// captureCookieName holds the signed token that authorizes a student to
	// publish as a specific peerName via /ws/peers/:name.
	captureCookieName = "grabber_capture"
	// captureTokenTTL bounds how long a single login stays valid.
	captureTokenTTL = 12 * time.Hour
)

func (s *Server) authEnabled() bool {
	return s.config.Security.AuthEnabled
}

func (s *Server) adminLogin() string {
	if s.config.Security.AdminLogin != nil && *s.config.Security.AdminLogin != "" {
		return *s.config.Security.AdminLogin
	}
	return "admin"
}

func (s *Server) findParticipant(name string) (config.ParticipantInfo, bool) {
	for _, p := range s.config.Security.Participants {
		if p.Name == name {
			return p, true
		}
	}
	return config.ParticipantInfo{}, false
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// captureScope is the message signed by the capture token's HMAC. Binding it to
// both peerName and expiry means a token issued for one student can't be reused
// for another peer or after it expires.
func captureScope(peerName string, exp int64) string {
	return "capture:" + peerName + ":" + strconv.FormatInt(exp, 10)
}

// signCaptureToken returns a cookie value of the form "<exp>.<sig>.<peerName>".
// exp and sig (base64url) contain no '.', so peerName (the remainder) may.
func (s *Server) signCaptureToken(peerName string, exp int64) string {
	sig := s.signUploadToken(captureScope(peerName, exp))
	return strconv.FormatInt(exp, 10) + "." + sig + "." + peerName
}

func (s *Server) verifyCaptureToken(token string) (peerName string, ok bool) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", false
	}
	if time.Now().Unix() > exp {
		return "", false
	}
	name := parts[2]
	if !s.verifyUploadToken(captureScope(name, exp), parts[1]) {
		return "", false
	}
	return name, true
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type loginResponse struct {
	Role     string `json:"role"`
	PeerName string `json:"peerName,omitempty"`
	Redirect string `json:"redirect"`
}

// handleLogin validates credentials and, for students, issues the capture cookie.
// Admins authenticate against AdminLogin + adminCredential; students against
// their participant name + per-participant password.
func (s *Server) handleLogin(c *fiber.Ctx) error {
	if !s.authEnabled() {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "auth is disabled"})
	}

	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	req.Login = strings.TrimSpace(req.Login)

	// Admin: reserved login + existing adminCredential as the password.
	if cred := s.config.Security.PlayerCredential; cred != nil &&
		req.Login == s.adminLogin() && constantTimeEqual(*cred, req.Password) {
		slog.Info("admin login", "login", req.Login)
		return c.JSON(loginResponse{Role: "admin", Redirect: "/admin"})
	}

	// Student: participant name as login + that participant's password.
	if p, ok := s.findParticipant(req.Login); ok && p.Password != "" &&
		constantTimeEqual(p.Password, req.Password) {
		exp := time.Now().Add(captureTokenTTL).Unix()
		c.Cookie(&fiber.Cookie{
			Name:     captureCookieName,
			Value:    s.signCaptureToken(p.Name, exp),
			Path:     "/",
			Expires:  time.Unix(exp, 0),
			HTTPOnly: true,
			SameSite: "Lax",
		})
		slog.Info("student login", "peerName", p.Name)
		return c.JSON(loginResponse{
			Role:     "student",
			PeerName: p.Name,
			Redirect: "/capture?peerName=" + url.QueryEscape(p.Name),
		})
	}

	slog.Warn("failed login attempt", "login", req.Login)
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid login or password"})
}

func (s *Server) handleLogout(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{
		Name:     captureCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Now().Add(-time.Hour),
		HTTPOnly: true,
	})
	return c.Redirect("/login")
}
