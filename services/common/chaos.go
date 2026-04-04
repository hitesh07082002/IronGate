package common

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ChaosState struct {
	mu        sync.RWMutex
	delay     time.Duration
	errorRate float64
	down      bool
	rng       *rand.Rand
}

func NewChaosState() *ChaosState {
	return &ChaosState{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func RegisterChaosHandlers(mux *http.ServeMux, state *ChaosState) {
	mux.HandleFunc("POST /chaos/latency", state.handleLatency)
	mux.HandleFunc("POST /chaos/errors", state.handleErrors)
	mux.HandleFunc("POST /chaos/down", state.handleDown)
	mux.HandleFunc("POST /chaos/reset", state.handleReset)
}

func (c *ChaosState) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/chaos/") {
			next.ServeHTTP(w, r)
			return
		}

		delay, errorRate, down := c.snapshot()
		if down {
			<-r.Context().Done()
			return
		}

		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()

			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
		}

		if errorRate > 0 && c.shouldFail(errorRate) {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "chaos injected error",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (c *ChaosState) handleLatency(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		DelayMS int `json:"delay_ms"`
	}
	if err := decodeJSON(r, &payload); err != nil || payload.DelayMS < 0 {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid delay_ms"})
		return
	}

	c.mu.Lock()
	c.delay = time.Duration(payload.DelayMS) * time.Millisecond
	c.mu.Unlock()

	WriteJSON(w, http.StatusOK, map[string]any{
		"delay_ms": payload.DelayMS,
	})
}

func (c *ChaosState) handleErrors(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Rate float64 `json:"rate"`
	}
	if err := decodeJSON(r, &payload); err != nil || payload.Rate < 0 || payload.Rate > 1 {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rate"})
		return
	}

	c.mu.Lock()
	c.errorRate = payload.Rate
	c.mu.Unlock()

	WriteJSON(w, http.StatusOK, map[string]any{
		"rate": payload.Rate,
	})
}

func (c *ChaosState) handleDown(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	c.down = true
	c.mu.Unlock()

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "down",
	})
}

func (c *ChaosState) handleReset(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	c.delay = 0
	c.errorRate = 0
	c.down = false
	c.mu.Unlock()

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "reset",
	})
}

func (c *ChaosState) snapshot() (time.Duration, float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.delay, c.errorRate, c.down
}

func (c *ChaosState) shouldFail(rate float64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rng.Float64() < rate
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
