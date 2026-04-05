package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const defaultReloadDebounce = 200 * time.Millisecond

type Watcher struct {
	configPath string
	debounce   time.Duration
	logger     *slog.Logger
	manager    *Manager
	ready      chan struct{}
	readyOnce  sync.Once
}

func NewWatcher(configPath string, manager *Manager, logger *slog.Logger, debounce time.Duration) (*Watcher, error) {
	if manager == nil {
		return nil, fmt.Errorf("config watcher requires a runtime manager")
	}
	if configPath == "" {
		return nil, fmt.Errorf("config watcher requires a config path")
	}

	absolutePath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}
	if debounce <= 0 {
		debounce = defaultReloadDebounce
	}

	return &Watcher{
		configPath: absolutePath,
		debounce:   debounce,
		logger:     logger,
		manager:    manager,
		ready:      make(chan struct{}),
	}, nil
}

func (w *Watcher) Run(ctx context.Context) error {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fsnotify watcher: %w", err)
	}
	defer fsWatcher.Close()

	if err := fsWatcher.Add(filepath.Dir(w.configPath)); err != nil {
		return fmt.Errorf("watch config directory: %w", err)
	}
	w.signalReady()

	var (
		timer   *time.Timer
		timerCh <-chan time.Time
	)
	defer func() {
		stopTimer(timer)
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-fsWatcher.Events:
			if !ok {
				return nil
			}
			if !matchesConfigPath(w.configPath, event.Name) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}

			timer = resetTimer(timer, w.debounce)
			timerCh = timer.C
		case err, ok := <-fsWatcher.Errors:
			if !ok {
				return nil
			}
			w.logger.Error("config watcher error", "path", w.configPath, "error", err)
		case <-timerCh:
			if err := w.manager.ReloadFromPath(w.configPath); err != nil {
				w.logger.Error("config reload failed; keeping previous runtime snapshot", "path", w.configPath, "error", err)
			} else {
				w.logger.Info("config reload succeeded", "path", w.configPath)
			}
			timerCh = nil
		}
	}
}

func (w *Watcher) Ready() <-chan struct{} {
	if w == nil || w.ready == nil {
		ready := make(chan struct{})
		close(ready)
		return ready
	}

	return w.ready
}

func (w *Watcher) signalReady() {
	if w == nil || w.ready == nil {
		return
	}

	w.readyOnce.Do(func() {
		close(w.ready)
	})
}

func matchesConfigPath(configPath, eventPath string) bool {
	return filepath.Clean(configPath) == filepath.Clean(eventPath)
}

func resetTimer(timer *time.Timer, delay time.Duration) *time.Timer {
	if timer == nil {
		return time.NewTimer(delay)
	}

	stopTimer(timer)
	timer.Reset(delay)
	return timer
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
