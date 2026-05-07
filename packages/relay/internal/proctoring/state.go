package proctoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	StatusIdle   Status = "idle"
	StatusActive Status = "active"
	StatusPaused Status = "paused"
)

const (
	minFps          = 1
	maxFps          = 60
	minChunkMs      = 100
	maxChunkMs      = 60_000
	minVideoBitrate = 10_000
	maxVideoBitrate = 50_000_000
)

type State struct {
	SessionId       string     `json:"sessionId"`
	Status          Status     `json:"status"`
	StartedAt       *time.Time `json:"startedAt"`
	EndsAt          *time.Time `json:"endsAt"`
	ChunkDurationMs uint32     `json:"chunkDurationMs"`
	Fps             uint32     `json:"fps"`
	VideoBitrate    uint32     `json:"videoBitrate"`
}

func (s State) IsActive() bool { return s.Status == StatusActive }
func (s State) IsPaused() bool { return s.Status == StatusPaused }
func (s State) IsIdle() bool   { return s.SessionId == "" || s.Status == StatusIdle }

func (s State) MarshalJSON() ([]byte, error) {
	type alias State
	return json.Marshal(struct {
		alias
		Active bool `json:"active"`
		Paused bool `json:"paused"`
	}{
		alias:  alias(s),
		Active: s.IsActive(),
		Paused: s.IsPaused(),
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	type alias State
	aux := &struct {
		*alias
		Active *bool `json:"active,omitempty"`
		Paused *bool `json:"paused,omitempty"`
	}{alias: (*alias)(s)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if s.Status == "" {
		switch {
		case aux.Active != nil && *aux.Active:
			s.Status = StatusActive
		case aux.Paused != nil && *aux.Paused:
			s.Status = StatusPaused
		default:
			s.Status = StatusIdle
		}
	}
	if s.SessionId == "" {
		s.Status = StatusIdle
	}
	return nil
}

type StartConfig struct {
	EndsAt          *time.Time
	ChunkDurationMs uint32
	Fps             uint32
	VideoBitrate    uint32
}

func (cfg StartConfig) Validate() error {
	if cfg.Fps < minFps || cfg.Fps > maxFps {
		return fmt.Errorf("%w: fps must be in [%d,%d], got %d", ErrInvalidConfig, minFps, maxFps, cfg.Fps)
	}
	if cfg.ChunkDurationMs < minChunkMs || cfg.ChunkDurationMs > maxChunkMs {
		return fmt.Errorf("%w: chunkDurationMs must be in [%d,%d], got %d", ErrInvalidConfig, minChunkMs, maxChunkMs, cfg.ChunkDurationMs)
	}
	if cfg.VideoBitrate < minVideoBitrate || cfg.VideoBitrate > maxVideoBitrate {
		return fmt.Errorf("%w: videoBitrate must be in [%d,%d], got %d", ErrInvalidConfig, minVideoBitrate, maxVideoBitrate, cfg.VideoBitrate)
	}
	if cfg.EndsAt != nil && !cfg.EndsAt.After(time.Now()) {
		return fmt.Errorf("%w: endsAt must be in the future", ErrInvalidConfig)
	}
	return nil
}

var (
	ErrAlreadyActive = errors.New("proctoring: session already running")
	ErrNoSession     = errors.New("proctoring: no active session")
	ErrNotPaused     = errors.New("proctoring: session is not paused")
	ErrNotActive     = errors.New("proctoring: session is not active")
	ErrInvalidConfig = errors.New("proctoring: invalid config")
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
	if err := cfg.Validate(); err != nil {
		return err
	}
	return m.transition(func(s State) (State, error) {
		if s.SessionId != "" {
			return s, ErrAlreadyActive
		}
		now := time.Now()
		return State{
			SessionId:       newSessionId(),
			Status:          StatusActive,
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
		if !s.IsActive() {
			return s, ErrNotActive
		}
		s.Status = StatusPaused
		return s, nil
	})
}

func (m *Manager) Resume() error {
	return m.transition(func(s State) (State, error) {
		if !s.IsPaused() {
			return s, ErrNotPaused
		}
		s.Status = StatusActive
		return s, nil
	})
}

func (m *Manager) Stop() error {
	return m.transition(func(s State) (State, error) {
		if s.SessionId == "" {
			return s, ErrNoSession
		}
		return State{Status: StatusIdle}, nil
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
	data, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	return writeFileSynced(m.statePath, data)
}

func writeFileSynced(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
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
