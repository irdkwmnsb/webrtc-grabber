package proctoring

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type State struct {
	SessionId       string     `json:"sessionId"`
	Active          bool       `json:"active"`
	Paused          bool       `json:"paused"`
	StartedAt       *time.Time `json:"startedAt"`
	EndsAt          *time.Time `json:"endsAt"`
	ChunkDurationMs uint32     `json:"chunkDurationMs"`
	Fps             uint32     `json:"fps"`
	VideoBitrate    uint32     `json:"videoBitrate"`
}

type StartConfig struct {
	EndsAt          *time.Time
	ChunkDurationMs uint32
	Fps             uint32
	VideoBitrate    uint32
}

var (
	ErrAlreadyActive = errors.New("proctoring: session already running")
	ErrNoSession     = errors.New("proctoring: no active session")
	ErrNotPaused     = errors.New("proctoring: session is not paused")
	ErrNotActive     = errors.New("proctoring: session is not active")
)

type Manager struct {
	mu            sync.RWMutex
	state         State
	statePath     string
	onChange      func(prev, next State)
	autoStopTimer *time.Timer
}

func NewManager(storageDir string, onChange func(prev, next State)) (*Manager, error) {
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{
		statePath: filepath.Join(storageDir, "proctoring_state.json"),
		onChange:  onChange,
	}
	if err := m.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	m.mu.Lock()
	m.scheduleAutoStopLocked()
	m.mu.Unlock()
	return m, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.autoStopTimer != nil {
		m.autoStopTimer.Stop()
		m.autoStopTimer = nil
	}
}

func (m *Manager) Get() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) Start(cfg StartConfig) error {
	return m.transition(func(s State) (State, error) {
		if s.SessionId != "" {
			return s, ErrAlreadyActive
		}
		now := time.Now()
		return State{
			SessionId:       newSessionId(),
			Active:          true,
			Paused:          false,
			StartedAt:       &now,
			EndsAt:          cfg.EndsAt,
			ChunkDurationMs: cfg.ChunkDurationMs,
			Fps:             cfg.Fps,
			VideoBitrate:    cfg.VideoBitrate,
		}, nil
	})
}

func (m *Manager) Pause() error {
	return m.transition(func(s State) (State, error) {
		if !s.Active {
			return s, ErrNotActive
		}
		s.Active = false
		s.Paused = true
		return s, nil
	})
}

func (m *Manager) Resume() error {
	return m.transition(func(s State) (State, error) {
		if !s.Paused {
			return s, ErrNotPaused
		}
		s.Active = true
		s.Paused = false
		return s, nil
	})
}

func (m *Manager) Stop() error {
	return m.transition(func(s State) (State, error) {
		if s.SessionId == "" {
			return s, ErrNoSession
		}
		return State{}, nil
	})
}

func (m *Manager) transition(fn func(State) (State, error)) error {
	m.mu.Lock()
	prev := m.state
	next, err := fn(prev)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	m.state = next
	if err := m.persistLocked(); err != nil {
		m.state = prev
		m.mu.Unlock()
		return err
	}
	m.scheduleAutoStopLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb(prev, next)
	}

	return nil
}

func (m *Manager) scheduleAutoStopLocked() {
	if m.autoStopTimer != nil {
		m.autoStopTimer.Stop()
		m.autoStopTimer = nil
	}
	if m.state.SessionId == "" || m.state.EndsAt == nil {
		return
	}
	delay := time.Until(*m.state.EndsAt)
	if delay < 0 {
		delay = 0
	}
	pinned := m.state.SessionId
	m.autoStopTimer = time.AfterFunc(delay, func() {
		m.mu.RLock()
		stillSame := m.state.SessionId == pinned
		m.mu.RUnlock()
		if !stillSame {
			return
		}
		_ = m.Stop()
	})
}

func (m *Manager) persistLocked() error {
	tmp := m.statePath + ".tmp"
	data, _ := json.Marshal(m.state)
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath)
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.state)
}

func newSessionId() string {
	return time.Now().Format("20060102_150405")
}
