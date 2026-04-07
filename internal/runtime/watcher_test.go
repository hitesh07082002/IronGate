package runtime

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewWatcherValidationAndReady(t *testing.T) {
	manager, err := NewManager(runtimeConfigForServer(t, "http://127.0.0.1:8081"), BuilderOptions{})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}

	if _, err := NewWatcher("", manager, nil, 0); err == nil {
		t.Fatal("expected empty config path to fail")
	}
	if _, err := NewWatcher("configs/gateway.yaml", nil, nil, 0); err == nil {
		t.Fatal("expected nil manager to fail")
	}

	watcher, err := NewWatcher("configs/gateway.yaml", manager, slog.New(slog.NewTextHandler(os.Stdout, nil)), 0)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	if !filepath.IsAbs(watcher.configPath) {
		t.Fatalf("expected absolute config path, got %q", watcher.configPath)
	}
	if watcher.debounce != defaultReloadDebounce {
		t.Fatalf("expected default debounce %s, got %s", defaultReloadDebounce, watcher.debounce)
	}

	select {
	case <-watcher.Ready():
		t.Fatal("expected watcher ready channel to remain open before signal")
	default:
	}

	watcher.signalReady()
	select {
	case <-watcher.Ready():
	default:
		t.Fatal("expected watcher ready channel to close after signal")
	}

	var nilWatcher *Watcher
	select {
	case <-nilWatcher.Ready():
	default:
		t.Fatal("expected nil watcher ready channel to already be closed")
	}
}

func TestWatcherHelpersAndRun(t *testing.T) {
	manager, err := NewManager(runtimeConfigForServer(t, "http://127.0.0.1:8081"), BuilderOptions{})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}

	configPath := writeRuntimeConfigFile(t, runtimeConfigForServer(t, "http://127.0.0.1:8081"))
	watcher, err := NewWatcher(configPath, manager, nil, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	if !matchesConfigPath(configPath, filepath.Clean(configPath)) {
		t.Fatal("expected cleaned config path to match")
	}
	if matchesConfigPath(configPath, configPath+".other") {
		t.Fatal("expected different config path not to match")
	}

	timer := resetTimer(nil, time.Millisecond)
	if timer == nil {
		t.Fatal("expected resetTimer to allocate a timer")
	}
	sameTimer := resetTimer(timer, time.Millisecond)
	if sameTimer != timer {
		t.Fatal("expected resetTimer to reuse the existing timer")
	}
	stopTimer(timer)
	stopTimer(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- watcher.Run(ctx)
	}()

	select {
	case <-watcher.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("expected watcher to signal ready")
	}

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("expected watcher run to stop cleanly, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected watcher run to stop after cancellation")
	}
}

func TestWatcherReadyConcurrent(t *testing.T) {
	manager, err := NewManager(runtimeConfigForServer(t, "http://127.0.0.1:8081"), BuilderOptions{})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}

	watcher, err := NewWatcher(writeRuntimeConfigFile(t, runtimeConfigForServer(t, "http://127.0.0.1:8081")), manager, nil, 0)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			watcher.signalReady()
			<-watcher.Ready()
		}()
	}

	close(start)
	wg.Wait()
}
