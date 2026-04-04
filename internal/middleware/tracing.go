package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const (
	HeaderRequestID = "X-Request-ID"
	HeaderUserID    = "X-User-ID"
	HeaderUserRole  = "X-User-Role"
)

func Tracing(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
