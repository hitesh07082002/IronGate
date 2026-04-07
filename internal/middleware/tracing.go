package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	HeaderRequestID = "X-Request-ID"
	HeaderUserID    = "X-User-ID"
	HeaderUserRole  = "X-User-Role"
)

func Tracing(logger *slog.Logger, tracer trace.Tracer) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("irongate.middleware.tracing")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, rootSpan := tracer.Start(req.Context(), "irongate.request")
			defer rootSpan.End()
			ctx, routeCapture := withRouteCapture(ctx)
			req = req.WithContext(ctx)

			ctx, tracingSpan := tracer.Start(ctx, "irongate.middleware.tracing")
			defer tracingSpan.End()
			req = req.WithContext(ctx)
			req.Header.Del(HeaderUserID)
			req.Header.Del(HeaderUserRole)
			req.Header.Del(HeaderRequestID)

			requestID := uuid.NewString()
			req.Header.Set(HeaderRequestID, requestID)

			recorder := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}
			recorder.Header().Set(HeaderRequestID, requestID)

			start := time.Now()
			logger.Info("request started",
				"method", req.Method,
				"path", req.URL.Path,
				"request_id", requestID,
			)

			next.ServeHTTP(recorder, req)

			routePath := req.URL.Path
			if routeCapture != nil && routeCapture.route != nil {
				routePath = routeCapture.route.Path
			}
			rootSpan.SetAttributes(
				attribute.String("request_id", requestID),
				attribute.String("http.method", req.Method),
				attribute.String("http.path", routePath),
				attribute.Int("http.status_code", recorder.statusCode),
			)
			if recorder.statusCode >= http.StatusInternalServerError {
				rootSpan.SetStatus(codes.Error, http.StatusText(recorder.statusCode))
			}

			logger.Info("request completed",
				"method", req.Method,
				"path", req.URL.Path,
				"request_id", requestID,
				"status", recorder.statusCode,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}

	r.statusCode = statusCode
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}

	return r.ResponseWriter.Write(data)
}
