package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
)

var jwtLikePattern = regexp.MustCompile(`^eyJ[A-Za-z0-9_+=/-]*\.[A-Za-z0-9_+=/-]*\.[A-Za-z0-9_+=/-]*$`)

type Event struct {
	TS      time.Time      `json:"ts"`
	Level   string         `json:"level"`
	Type    string         `json:"type"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

type EventHub struct {
	logger *slog.Logger

	mu      sync.Mutex
	nextID  int
	buffer  []Event
	clients map[int]chan Event
	rng     *rand.Rand
}

func NewEventHub(logger *slog.Logger) *EventHub {
	if logger == nil {
		logger = slog.Default()
	}

	return &EventHub{
		logger:  logger,
		buffer:  make([]Event, 0, 256),
		clients: make(map[int]chan Event),
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (h *EventHub) Publish(event Event) {
	if h == nil {
		return
	}
	if event.TS.IsZero() {
		event.TS = time.Now().UTC()
	}

	h.mu.Lock()
	h.pruneLocked(event.TS)
	h.buffer = append(h.buffer, event)

	clientChans := make([]chan Event, 0, len(h.clients))
	for _, client := range h.clients {
		clientChans = append(clientChans, client)
	}
	h.mu.Unlock()

	for _, client := range clientChans {
		select {
		case client <- event:
		default:
			select {
			case <-client:
			default:
			}
			select {
			case client <- event:
			default:
			}
			h.logger.Warn("dropping oldest SSE event for slow client")
		}
	}
}

func (h *EventHub) Subscribe() ([]Event, <-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pruneLocked(time.Now().UTC())
	snapshot := append([]Event(nil), h.buffer...)
	ch := make(chan Event, 256)
	id := h.nextID
	h.nextID++
	h.clients[id] = ch

	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if existing, ok := h.clients[id]; ok {
			delete(h.clients, id)
			close(existing)
		}
	}

	return snapshot, ch, cancel
}

func (h *EventHub) pruneLocked(now time.Time) {
	cutoff := now.Add(-5 * time.Minute)
	trimmed := h.buffer[:0]
	for _, event := range h.buffer {
		if event.TS.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, event)
	}
	h.buffer = trimmed
}

func (a *app) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	snapshot, eventsCh, cancel := a.eventHub.Subscribe()
	defer cancel()

	for _, event := range snapshot {
		if err := writeSSEEvent(w, event); err != nil {
			return
		}
	}
	flusher.Flush()

	keepAliveTicker := time.NewTicker(15 * time.Second)
	defer keepAliveTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAliveTicker.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-eventsCh:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSEEvent(w io.Writer, event Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, "data: "+string(raw)+"\n\n")
	return err
}

func (a *app) streamGatewayEvents(ctx context.Context) {
	backoff := time.Second
	since := time.Now().UTC()

	for {
		if ctx.Err() != nil {
			return
		}

		containerID, err := a.gatewayContainerID(ctx)
		if err != nil {
			a.logger.Warn("failed to resolve gateway container", "error", err)
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		logs, err := a.docker.ContainerLogs(ctx, containerID, dockercontainer.LogsOptions{
			ShowStdout: true,
			ShowStderr: false,
			Follow:     true,
			Since:      since.Format(time.RFC3339Nano),
		})
		if err != nil {
			a.logger.Warn("failed to stream gateway logs", "error", err)
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		err = parseEvents(logs, func(event Event) {
			since = event.TS
			if !a.shouldSampleEvent(event.Type) {
				return
			}
			a.eventHub.Publish(event)
		})
		logs.Close()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, io.EOF) {
			a.logger.Warn("gateway event parser stopped", "error", err)
		}

		if !sleepContext(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func (a *app) gatewayContainerID(ctx context.Context) (string, error) {
	override := strings.TrimSpace(a.gatewayContainerName)
	if override != "" {
		inspect, err := a.docker.ContainerInspect(ctx, override)
		if err == nil {
			return inspect.ID, nil
		}
	}

	filterArgs := dockerfilters.NewArgs(
		dockerfilters.Arg("label", "com.docker.compose.project="+a.composeProject),
		dockerfilters.Arg("label", "com.docker.compose.service=gateway"),
	)

	containers, err := a.docker.ContainerList(ctx, dockercontainer.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", errors.New("gateway container not found")
	}

	return containers[0].ID, nil
}

func parseEvents(reader io.Reader, emit func(Event)) error {
	if reader == nil {
		return nil
	}

	lineWriter := &eventLineWriter{
		handleLine: func(line []byte) error {
			event, ok, err := parseEventLine(line)
			if err != nil || !ok {
				return err
			}
			emit(event)
			return nil
		},
	}

	if _, err := stdcopy.StdCopy(lineWriter, io.Discard, reader); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	return lineWriter.Flush()
}

type eventLineWriter struct {
	buffer     bytes.Buffer
	handleLine func([]byte) error
}

func (w *eventLineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		index := bytes.IndexByte(p, '\n')
		if index < 0 {
			_, _ = w.buffer.Write(p)
			break
		}

		_, _ = w.buffer.Write(p[:index])
		line := bytes.TrimSpace(append([]byte(nil), w.buffer.Bytes()...))
		w.buffer.Reset()
		if len(line) > 0 {
			if err := w.handleLine(line); err != nil {
				return 0, err
			}
		}
		p = p[index+1:]
	}

	return total, nil
}

func (w *eventLineWriter) Flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}

	line := bytes.TrimSpace(append([]byte(nil), w.buffer.Bytes()...))
	w.buffer.Reset()
	if len(line) == 0 {
		return nil
	}

	return w.handleLine(line)
}

func parseEventLine(line []byte) (Event, bool, error) {
	var raw struct {
		Time  string         `json:"time"`
		Level string         `json:"level"`
		Msg   string         `json:"msg"`
		Type  string         `json:"type"`
		Attrs map[string]any `json:"attrs"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, false, nil
	}
	if strings.TrimSpace(raw.Type) == "" {
		return Event{}, false, nil
	}

	ts := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, raw.Time); err == nil {
		ts = parsed.UTC()
	}

	return Event{
		TS:      ts,
		Level:   strings.TrimSpace(raw.Level),
		Type:    strings.TrimSpace(raw.Type),
		Message: strings.TrimSpace(raw.Msg),
		Attrs:   sanitizeParsedAttrs(raw.Attrs),
	}, true, nil
}

func sanitizeParsedAttrs(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return map[string]any{}
	}

	sanitized := make(map[string]any, len(attrs))
	for key, value := range attrs {
		sanitized[key] = sanitizeParsedValue(value)
	}

	return sanitized
}

func sanitizeParsedValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeParsedString(typed)
	case []any:
		out := make([]any, len(typed))
		for index, entry := range typed {
			out[index] = sanitizeParsedValue(entry)
		}
		return out
	case map[string]any:
		return sanitizeParsedAttrs(typed)
	default:
		return value
	}
}

func sanitizeParsedString(value string) string {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return value
	case jwtLikePattern.MatchString(trimmed):
		return "[redacted-jwt]"
	case trimmed == strings.TrimSpace(os.Getenv(demoTokenEnvVar)):
		return "[redacted-secret]"
	case trimmed == strings.TrimSpace(os.Getenv(adminTokenEnvVar)):
		return "[redacted-secret]"
	default:
		return value
	}
}

func (a *app) shouldSampleEvent(eventType string) bool {
	switch eventType {
	case "request_success", "request_routed":
		return a.samplePercent(0.01)
	case "circuit_rejected", "upstream_5xx":
		return a.samplePercent(0.05)
	default:
		return true
	}
}

func (a *app) samplePercent(rate float64) bool {
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}

	a.eventHub.mu.Lock()
	defer a.eventHub.mu.Unlock()
	return a.eventHub.rng.Float64() < rate
}

func nextBackoff(current time.Duration) time.Duration {
	switch current {
	case time.Second:
		return 2 * time.Second
	case 2 * time.Second:
		return 5 * time.Second
	default:
		return 30 * time.Second
	}
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
