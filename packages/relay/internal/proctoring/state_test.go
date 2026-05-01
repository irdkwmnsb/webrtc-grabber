package proctoring

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartStop(t *testing.T) {
	var captured []State
	m, err := NewManager(t.TempDir(), func(prev, next State) {
		captured = append(captured, next)
	})
	require.NoError(t, err)

	require.NoError(t, m.Start(StartConfig{
		Fps:             1,
		ChunkDurationMs: 1000,
	}))

	require.True(t, m.Get().Active)
	require.NotEmpty(t, m.Get().SessionId)

	require.ErrorIs(t, m.Start(StartConfig{}), ErrAlreadyActive)

	require.NoError(t, m.Pause())
	require.NoError(t, m.Resume())
	require.NoError(t, m.Stop())
	require.Equal(t, "", m.Get().SessionId)
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	m1, _ := NewManager(dir, nil)
	require.NoError(t, m1.Start(StartConfig{
		Fps: 1,
	}))
	sid := m1.Get().SessionId

	m2, err := NewManager(dir, nil)
	require.NoError(t, err)
	require.Equal(t, sid, m2.Get().SessionId)
	require.True(t, m2.Get().Active)
}
