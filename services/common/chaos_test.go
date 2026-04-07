package common

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisterChaosHandlersUpdateAndResetState(t *testing.T) {
	state := NewChaosState()
	mux := http.NewServeMux()
	RegisterChaosHandlers(mux, state)

	latencyResp := postJSON(t, mux, "/chaos/latency", `{"delay_ms":25}`)
	if latencyResp.Code != http.StatusOK {
		t.Fatalf("expected latency handler 200, got %d", latencyResp.Code)
	}
	if delay, rate, down := state.snapshot(); delay != 25*time.Millisecond || rate != 0 || down {
		t.Fatalf("unexpected latency snapshot: delay=%s rate=%v down=%t", delay, rate, down)
	}

	errorsResp := postJSON(t, mux, "/chaos/errors", `{"rate":0.5}`)
	if errorsResp.Code != http.StatusOK {
		t.Fatalf("expected errors handler 200, got %d", errorsResp.Code)
	}
	if delay, rate, down := state.snapshot(); delay != 25*time.Millisecond || rate != 0.5 || down {
		t.Fatalf("unexpected errors snapshot: delay=%s rate=%v down=%t", delay, rate, down)
	}

	downResp := postJSON(t, mux, "/chaos/down", `{}`)
	if downResp.Code != http.StatusOK {
		t.Fatalf("expected down handler 200, got %d", downResp.Code)
	}
	if _, _, down := state.snapshot(); !down {
		t.Fatal("expected down handler to mark the service down")
	}

	resetResp := postJSON(t, mux, "/chaos/reset", `{}`)
	if resetResp.Code != http.StatusOK {
		t.Fatalf("expected reset handler 200, got %d", resetResp.Code)
	}
	if delay, rate, down := state.snapshot(); delay != 0 || rate != 0 || down {
		t.Fatalf("expected reset state, got delay=%s rate=%v down=%t", delay, rate, down)
	}
}

func TestChaosHandlersRejectInvalidPayloads(t *testing.T) {
	state := NewChaosState()
	mux := http.NewServeMux()
	RegisterChaosHandlers(mux, state)

	if resp := postJSON(t, mux, "/chaos/latency", `{"delay_ms":-1}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid latency payload to return 400, got %d", resp.Code)
	}
	if resp := postJSON(t, mux, "/chaos/errors", `{"rate":2}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid error rate payload to return 400, got %d", resp.Code)
	}
}

func TestChaosMiddlewareDelaysAndInjectsErrors(t *testing.T) {
	state := NewChaosState()
	mux := http.NewServeMux()
	RegisterChaosHandlers(mux, state)

	var nextCalls atomic.Int32
	mux.Handle("GET /users", state.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls.Add(1)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})))

	if resp := postJSON(t, mux, "/chaos/latency", `{"delay_ms":10}`); resp.Code != http.StatusOK {
		t.Fatalf("expected latency update 200, got %d", resp.Code)
	}

	start := time.Now()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/users", nil))
	if elapsed := time.Since(start); elapsed < 8*time.Millisecond {
		t.Fatalf("expected chaos delay to be applied, got %s", elapsed)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected delayed request to reach next handler, got %d", recorder.Code)
	}
	if nextCalls.Load() != 1 {
		t.Fatalf("expected next handler once after delay, got %d", nextCalls.Load())
	}

	if resp := postJSON(t, mux, "/chaos/reset", `{}`); resp.Code != http.StatusOK {
		t.Fatalf("expected reset 200, got %d", resp.Code)
	}
	if resp := postJSON(t, mux, "/chaos/errors", `{"rate":1}`); resp.Code != http.StatusOK {
		t.Fatalf("expected error-rate update 200, got %d", resp.Code)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/users", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected injected chaos error, got %d", recorder.Code)
	}
	if nextCalls.Load() != 1 {
		t.Fatalf("expected chaos error to short-circuit next handler, got %d calls", nextCalls.Load())
	}
}

func TestChaosMiddlewareDownWaitsForCancellation(t *testing.T) {
	state := NewChaosState()
	state.down = true

	var nextCalls atomic.Int32
	handler := state.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls.Add(1)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/users", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if nextCalls.Load() != 0 {
		t.Fatalf("expected down mode to suppress next handler, got %d calls", nextCalls.Load())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected down mode to return no body, got %q", recorder.Body.String())
	}
}

func TestChaosMiddlewareBypassesChaosRoutes(t *testing.T) {
	state := NewChaosState()
	state.errorRate = 1
	state.down = true

	var nextCalls atomic.Int32
	handler := state.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls.Add(1)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/chaos/reset", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected /chaos/ routes to bypass injection, got %d", recorder.Code)
	}
	if nextCalls.Load() != 1 {
		t.Fatalf("expected /chaos path to reach next handler, got %d calls", nextCalls.Load())
	}
}

func TestChaosStateConcurrencyRace(t *testing.T) {
	state := NewChaosState()
	mux := http.NewServeMux()
	RegisterChaosHandlers(mux, state)

	var nextCalls atomic.Int32
	handler := state.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalls.Add(1)
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))

	const (
		requestWorkers = 100
		mutatorWorkers = 4
		iterations     = 8
	)

	start := make(chan struct{})
	errs := make(chan string, requestWorkers*iterations)
	var wg sync.WaitGroup

	for range requestWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			for range iterations {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				req := httptest.NewRequest(http.MethodGet, "/orders", nil).WithContext(ctx)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				cancel()

				switch recorder.Code {
				case 0, http.StatusOK, http.StatusInternalServerError:
				default:
					errs <- recorder.Body.String()
					return
				}
			}
		}()
	}

	mutators := []struct {
		path string
		body string
	}{
		{path: "/chaos/latency", body: `{"delay_ms":1}`},
		{path: "/chaos/errors", body: `{"rate":0.5}`},
		{path: "/chaos/down", body: `{}`},
		{path: "/chaos/reset", body: `{}`},
	}
	for index := 0; index < mutatorWorkers; index++ {
		mutator := mutators[index]
		wg.Add(1)
		go func(path, body string) {
			defer wg.Done()
			<-start

			for range iterations {
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				mux.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusOK {
					errs <- recorder.Body.String()
					return
				}
			}
		}(mutator.path, mutator.body)
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("unexpected chaos concurrency error: %s", err)
	}
	if nextCalls.Load() == 0 {
		t.Fatal("expected concurrent requests to reach the wrapped handler at least once")
	}
}

func TestShouldFailAndDecodeJSON(t *testing.T) {
	state := &ChaosState{rng: rand.New(rand.NewSource(0))}
	if state.shouldFail(0) {
		t.Fatal("expected zero chaos rate not to fail")
	}
	if !state.shouldFail(1) {
		t.Fatal("expected 100% chaos rate to fail")
	}

	req := httptest.NewRequest(http.MethodPost, "/chaos/errors", bytes.NewBufferString(`{"rate":0.2,"extra":true}`))
	var payload struct {
		Rate float64 `json:"rate"`
	}
	if err := decodeJSON(req, &payload); err == nil {
		t.Fatal("expected decodeJSON to reject unknown fields")
	}
}

func TestHealthHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	HealthHandler("user-service").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected health handler 200, got %d", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" || body["service"] != "user-service" {
		t.Fatalf("unexpected health payload %#v", body)
	}
}

func postJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
