package config

import (
	"log/slog"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Manager struct {
	mu           sync.RWMutex
	current      *AppConfig
	configDir    string
	watchFiles   []string
	onUpdateFunc func(*AppConfig)
}

func NewManager(configDir string) (*Manager, error) {
	mgr := &Manager{
		configDir:  configDir,
		watchFiles: []string{"server.yaml", "webrtc.yaml", "security.yaml"},
	}

	if err := mgr.Reload(); err != nil {
		return nil, err
	}

	go mgr.startWatcher()

	return mgr, nil
}

func (m *Manager) Get() AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.current
}

func (m *Manager) Reload() error {
	newConfig, err := LoadAppConfig(m.configDir)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.current = newConfig
	m.mu.Unlock()

	if m.onUpdateFunc != nil {
		m.onUpdateFunc(newConfig)
	}

	slog.Info("configuration reloaded successfully")
	return nil
}

func (m *Manager) SetUpdateCallback(f func(*AppConfig)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdateFunc = f
}

func (m *Manager) startWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("failed to create config watcher", "error", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(m.configDir); err != nil {
		slog.Error("failed to watch config dir", "error", err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				slog.Info("config file modified", "file", event.Name)
				if err := m.Reload(); err != nil {
					slog.Error("error reloading config", "error", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("config watcher error", "error", err)
		}
	}
}
