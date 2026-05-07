package signalling

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const uploadTokenHeader = "X-Upload-Token"

func newUploadSecret(configured *string) ([]byte, bool, error) {
	if configured != nil && *configured != "" {
		return []byte(*configured), true, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, false, fmt.Errorf("generate upload secret: %w", err)
	}
	return b, false, nil
}

func (s *Server) signUploadToken(scope string) string {
	h := hmac.New(sha256.New, s.uploadSecret)
	h.Write([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *Server) verifyUploadToken(scope, token string) bool {
	if token == "" {
		return false
	}
	expected := s.signUploadToken(scope)
	return hmac.Equal([]byte(expected), []byte(token))
}

func proctoringScope(sessionId, peerName string) string {
	return "proctoring:" + sessionId + ":" + peerName
}

func recordScope(peerName string) string {
	return "record:" + peerName
}
