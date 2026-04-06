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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hitesh07082002/irongate/internal/config"
)

func TestRouterMatchesLongestPrefixAndStoresRouteConfig(t *testing.T) {
	routes := []config.RouteConfig{
		{Path: "/api/users", Service: "user-service", Methods: []string{http.MethodGet}},
		{Path: "/api/users/login", Service: "login-service", Methods: []string{http.MethodPost}},
	}

	var gotRoute *config.RouteConfig
	handler := Router(routes, nil)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
	handler := Router([]config.RouteConfig{{Path: "/api/users", Service: "user-service"}}, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
	}}, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
	handler := Tracing(logger, nil)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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

func TestUnsupportedFeaturesAllowsRoutesThroughNowThatRetryIsLive(t *testing.T) {
	nextCalled := false
	handler := UnsupportedFeatures()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	req = req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
		Path:    "/api/orders",
		Service: "order-service",
		Retry:   config.RetryConfig{MaxAttempts: 2},
		RateLimit: &config.RateLimitConfig{
			Requests: 10,
			Window:   time.Minute,
		},
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if !nextCalled {
		t.Fatal("expected retry-configured route to pass through unsupported middleware")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
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
	handler := Tracing(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestTracingRecordsRootSpanWithRouteTemplate(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	handler := Tracing(testRateLimitLogger(), tp.Tracer("irongate.middleware.tracing"))(
		Router([]config.RouteConfig{
			{Path: "/api/users", Service: "user-service", Methods: []string{http.MethodGet}},
		}, tp.Tracer("irongate.middleware.router"))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		})),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	root := findEndedSpanByName(t, recorder.Ended(), "irongate.request")
	if got := spanAttribute(root.Attributes(), "http.path"); got != "/api/users" {
		t.Fatalf("expected root http.path to use route template, got %q", got)
	}
	if got := spanAttribute(root.Attributes(), "http.method"); got != http.MethodGet {
		t.Fatalf("expected root http.method %q, got %q", http.MethodGet, got)
	}
	if got := spanAttribute(root.Attributes(), "http.status_code"); got != int64(http.StatusCreated) {
		t.Fatalf("expected root http.status_code %d, got %v", http.StatusCreated, got)
	}
	if spanAttribute(root.Attributes(), "request_id") == "" {
		t.Fatal("expected root span request_id attribute")
	}

	routerSpan := findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.router")
	if got := spanAttribute(routerSpan.Attributes(), "route.service"); got != "user-service" {
		t.Fatalf("expected route.service user-service, got %q", got)
	}
	if got := spanAttribute(routerSpan.Attributes(), "route.path"); got != "/api/users" {
		t.Fatalf("expected route.path /api/users, got %q", got)
	}
	if got := spanAttribute(routerSpan.Attributes(), "route.matched"); got != true {
		t.Fatalf("expected route.matched true, got %v", got)
	}

	findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.tracing")
}

func TestRouterMarksNoMatchAsError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	handler := Router([]config.RouteConfig{{Path: "/api/users", Service: "user-service"}}, tp.Tracer("irongate.middleware.router"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected unmatched route to stop before next handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	span := findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.router")
	if got := spanAttribute(span.Attributes(), "route.matched"); got != false {
		t.Fatalf("expected route.matched false, got %v", got)
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("expected router span status error, got %s", span.Status().Code)
	}
}

func findEndedSpanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	t.Fatalf("span %q not found", name)
	return nil
}

func spanAttribute(attrs []attribute.KeyValue, key string) any {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInterface()
		}
	}

	return nil
}
