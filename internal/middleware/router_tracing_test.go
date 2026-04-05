package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
)

func TestRouterMatchesLongestPrefixAndStoresRouteConfig(t *testing.T) {
	routes := []config.RouteConfig{
		{Path: "/api/users", Service: "user-service", Methods: []string{http.MethodGet}},
		{Path: "/api/users/login", Service: "login-service", Methods: []string{http.MethodPost}},
	}

	var gotRoute *config.RouteConfig
	handler := Router(routes)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotRoute = GetRouteConfig(req)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/users/login", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	if gotRoute == nil || gotRoute.Path != "/api/users/login" {
		t.Fatalf("expected longest-prefix route in context, got %+v", gotRoute)
	}
}

func TestRouterReturnsNotFoundForUnknownRoute(t *testing.T) {
	handler := Router([]config.RouteConfig{{Path: "/api/users", Service: "user-service"}})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected unmatched route to stop before next handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestRouterReturnsMethodNotAllowedAndAllowHeader(t *testing.T) {
	handler := Router([]config.RouteConfig{{
		Path:    "/api/users",
		Service: "user-service",
		Methods: []string{http.MethodGet, http.MethodPost},
	}})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected disallowed method to stop before next handler")
	}))

	req := httptest.NewRequest(http.MethodDelete, "/api/users", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Allow"); got != "GET, POST" {
		t.Fatalf("expected Allow header to list permitted methods, got %q", got)
	}
}

func TestTracingSanitizesHeadersPropagatesRequestIDAndLogsCompletion(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	var seenRequestID string
	handler := Tracing(logger)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(HeaderUserID) != "" {
			t.Fatal("expected tracing to remove user id header before downstream handlers")
		}
		if req.Header.Get(HeaderUserRole) != "" {
			t.Fatal("expected tracing to remove user role header before downstream handlers")
		}
		seenRequestID = req.Header.Get(HeaderRequestID)
		if seenRequestID == "" {
			t.Fatal("expected tracing to attach a request id")
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set(HeaderUserID, "u-1")
	req.Header.Set(HeaderUserRole, "admin")
	req.Header.Set(HeaderRequestID, "client-supplied")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", recorder.Code)
	}
	if got := recorder.Header().Get(HeaderRequestID); got == "" || got != seenRequestID {
		t.Fatalf("expected response request id %q to match downstream value %q", got, seenRequestID)
	}
	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "request started") || !strings.Contains(logOutput, "request completed") {
		t.Fatalf("expected tracing logs for request lifecycle, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "status=201") {
		t.Fatalf("expected completion log to include status code, got %q", logOutput)
	}
}

func TestStatusRecorderDefaultsToOKAndIgnoresSecondWriteHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	status := &statusRecorder{ResponseWriter: recorder, statusCode: http.StatusOK}

	if _, err := status.Write([]byte("ok")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	status.WriteHeader(http.StatusCreated)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected first implicit status to stick, got %d", recorder.Code)
	}
	if status.Unwrap() != recorder {
		t.Fatal("expected Unwrap to return original response writer")
	}
}

func TestUnsupportedFeaturesBlocksRetryAndAllowsRateLimitOnlyRoutes(t *testing.T) {
	t.Run("retry is still blocked", func(t *testing.T) {
		handler := UnsupportedFeatures()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("expected retry-configured route to stop before next handler")
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		req = req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
			Path:    "/api/orders",
			Service: "order-service",
			Retry:   config.RetryConfig{MaxAttempts: 2},
		}))

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("expected 501, got %d", recorder.Code)
		}
	})

	t.Run("rate limit no longer trips unsupported middleware", func(t *testing.T) {
		nextCalled := false
		handler := UnsupportedFeatures()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
		req = req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
			Path:    "/api/orders",
			Service: "order-service",
			RateLimit: &config.RateLimitConfig{
				Requests: 10,
				Window:   time.Minute,
			},
		}))

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		if !nextCalled {
			t.Fatal("expected rate-limited route to pass through unsupported middleware")
		}
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", recorder.Code)
		}
	})
}

func TestUnsupportedFeaturesRequiresRouteConfig(t *testing.T) {
	handler := UnsupportedFeatures()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected missing route config to stop before next handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
}

func TestRetryAfterSecondsNeverDropsBelowOne(t *testing.T) {
	now := time.Now()

	if got := retryAfterSeconds(now, now.Add(250*time.Millisecond)); got != 1 {
		t.Fatalf("expected sub-second retry-after rounded up to 1, got %d", got)
	}
	if got := retryAfterSeconds(now, now.Add(3*time.Second)); got != 3 {
		t.Fatalf("expected whole-second retry-after preserved, got %d", got)
	}
}

func TestMethodAllowedTreatsEmptyListAsAllowAll(t *testing.T) {
	if !methodAllowed(nil, http.MethodPatch) {
		t.Fatal("expected empty allowed-method list to permit request")
	}
	if methodAllowed([]string{http.MethodGet}, http.MethodPost) {
		t.Fatal("expected non-matching method to be rejected")
	}
}

func TestGetRouteConfigHandlesMissingValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	if got := GetRouteConfig(req); got != nil {
		t.Fatalf("expected nil route config, got %+v", got)
	}
}

func TestTracingFallsBackToDefaultLogger(t *testing.T) {
	handler := Tracing(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	if recorder.Header().Get(HeaderRequestID) == "" {
		t.Fatal("expected default logger tracing path to still assign request id")
	}
}
