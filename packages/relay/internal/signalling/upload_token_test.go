package signalling

import (
	"testing"
)

func TestUploadToken_Roundtrip(t *testing.T) {
	e := newTestEnv(t)
	scope := proctoringScope("session-1", "peer-A", "desktop")
	tok := e.signToken(scope)
	if !e.srv.verifyUploadToken(scope, tok) {
		t.Fatal("verifyUploadToken rejected a freshly signed token")
	}
}

func TestUploadToken_RejectsWrongScope(t *testing.T) {
	e := newTestEnv(t)
	tok := e.signToken(proctoringScope("session-1", "peer-A", "desktop"))
	for _, wrong := range []string{
		proctoringScope("session-2", "peer-A", "desktop"),
		proctoringScope("session-1", "peer-B", "desktop"),
		proctoringScope("session-1", "peer-A", "webcam"),
		recordScope("peer-A"),
		"unrelated-scope",
	} {
		if e.srv.verifyUploadToken(wrong, tok) {
			t.Fatalf("verifyUploadToken accepted token with scope %q (should be rejected)", wrong)
		}
	}
}

func TestUploadToken_RejectsEmptyAndGarbage(t *testing.T) {
	e := newTestEnv(t)
	scope := proctoringScope("s", "p", "desktop")
	for _, bad := range []string{"", "garbage", "definitely-not-a-real-token"} {
		if e.srv.verifyUploadToken(scope, bad) {
			t.Fatalf("verifyUploadToken accepted invalid token %q", bad)
		}
	}
}

func TestUploadToken_RejectsForeignSecret(t *testing.T) {
	e := newTestEnv(t)
	scope := proctoringScope("s", "p", "desktop")
	// Sign with a foreign secret — should not validate against the server.
	foreign := e2eSign("totally-different-secret", scope)
	if e.srv.verifyUploadToken(scope, foreign) {
		t.Fatal("verifyUploadToken accepted a token signed with a foreign secret")
	}
}

func TestProctoringUploadTokens_AllStreamKeys(t *testing.T) {
	e := newTestEnv(t)
	tokens := e.srv.proctoringUploadTokens("session-1", "peer-A")
	if len(tokens) != 2 {
		t.Fatalf("expected tokens for desktop and webcam, got %d: %v", len(tokens), tokens)
	}
	for streamKey, tok := range tokens {
		if !e.srv.verifyUploadToken(proctoringScope("session-1", "peer-A", streamKey), tok) {
			t.Fatalf("token for stream %q did not validate", streamKey)
		}
	}
}
