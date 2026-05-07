package proctoring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func validStartConfig() StartConfig {
	return StartConfig{
		Fps:             1,
		ChunkDurationMs: 1000,
		VideoBitrate:    minVideoBitrate,
	}
}

func TestStartStop(t *testing.T) {
	var captured []State
	m, err := NewManager(t.TempDir(), func(prev, next State) {
		captured = append(captured, next)
	})
	require.NoError(t, err)

	require.NoError(t, m.Start(validStartConfig()))

	require.True(t, m.Get().IsActive())
	require.NotEmpty(t, m.Get().SessionId)

	require.ErrorIs(t, m.Start(validStartConfig()), ErrAlreadyActive)

	require.NoError(t, m.Pause())
	require.True(t, m.Get().IsPaused())
	require.NoError(t, m.Resume())
	require.True(t, m.Get().IsActive())
	require.NoError(t, m.Stop())
	require.True(t, m.Get().IsIdle())
	require.Equal(t, "", m.Get().SessionId)
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir, nil)
	require.NoError(t, m1.Start(validStartConfig()))
	sid := m1.Get().SessionId

	m2, err := NewManager(dir, nil)
	require.NoError(t, err)
	require.Equal(t, sid, m2.Get().SessionId)
	require.True(t, m2.Get().IsActive())
}

func TestStartConfigValidation(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir, nil)

	require.ErrorIs(t, m.Start(StartConfig{Fps: 0, ChunkDurationMs: 1000, VideoBitrate: minVideoBitrate}), ErrInvalidConfig)
	require.ErrorIs(t, m.Start(StartConfig{Fps: 1, ChunkDurationMs: 0, VideoBitrate: minVideoBitrate}), ErrInvalidConfig)
	require.ErrorIs(t, m.Start(StartConfig{Fps: 1, ChunkDurationMs: 1000, VideoBitrate: 0}), ErrInvalidConfig)
}

func TestLoadLegacyActivePausedJSON(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`{"sessionId":"abc","active":true,"paused":false,"chunkDurationMs":1000,"fps":1,"videoBitrate":10000}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "proctoring_state.json"), legacy, 0o644))

	m, err := NewManager(dir, nil)
	require.NoError(t, err)
	require.True(t, m.Get().IsActive())
	require.Equal(t, "abc", m.Get().SessionId)
}

func TestMarshalEmitsCompatFields(t *testing.T) {
	s := State{SessionId: "x", Status: StatusPaused}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, "paused", raw["status"])
	require.Equal(t, false, raw["active"])
	require.Equal(t, true, raw["paused"])
}
