package middleware

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/response"
)

const (
	HeaderRateLimitLimit     = "X-RateLimit-Limit"
	HeaderRateLimitRemaining = "X-RateLimit-Remaining"
	HeaderRateLimitReset     = "X-RateLimit-Reset"
	HeaderRetryAfter         = "Retry-After"

	rateLimitExceededMessage            = "rate limit exceeded"
	rateLimitMetadataMissingMessage     = "rate limit metadata missing"
	rateLimitStrategyUnsupportedMessage = "route rate limit strategy is not implemented yet"
	defaultRateLimitStrategy            = "sliding_window"
)

type RateLimiterOptions struct {
	TrustedProxies []netip.Prefix
}

func RateLimiter(store ratelimit.Store, logger *slog.Logger, options RateLimiterOptions) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			route := GetRouteConfig(req)
			if route == nil {
				response.WriteError(w, req, http.StatusInternalServerError, "route config missing")
				return
			}

			if route.RateLimit == nil {
				next.ServeHTTP(w, req)
				return
			}

			strategy := strings.TrimSpace(route.RateLimit.Strategy)
			if strategy == "" {
				strategy = defaultRateLimitStrategy
			}
			if strategy != defaultRateLimitStrategy {
				response.WriteError(w, req, http.StatusNotImplemented, rateLimitStrategyUnsupportedMessage)
				return
			}

			clientKey := rateLimitClientKey(req, options.TrustedProxies)
			requestID := strings.TrimSpace(req.Header.Get(HeaderRequestID))
			if clientKey == "" || requestID == "" {
				response.WriteError(w, req, http.StatusInternalServerError, rateLimitMetadataMissingMessage)
				return
			}

			if store == nil {
				logger.Warn("rate limit store unavailable; allowing request",
					"path", req.URL.Path,
					"route", route.Path,
					"request_id", response.RequestID(req),
					"client_key", clientKey,
					"reason", "store not configured",
				)
				next.ServeHTTP(w, req)
				return
			}

			now := time.Now()
			decision, err := store.Allow(req.Context(), ratelimit.Request{
				Key:      ratelimit.Key(clientKey, route.Path),
				Limit:    route.RateLimit.Requests,
				Window:   route.RateLimit.Window,
				Strategy: strategy,
				Member:   requestID,
				Now:      now,
			})
			if err != nil {
				logger.Warn("rate limit store unavailable; allowing request",
					"path", req.URL.Path,
					"route", route.Path,
					"request_id", response.RequestID(req),
					"client_key", clientKey,
					"error", err,
				)
				next.ServeHTTP(w, req)
				return
			}

			setRateLimitHeaders(w.Header(), route.RateLimit.Requests, decision)
			if !decision.Allowed {
				w.Header().Set(HeaderRetryAfter, strconv.FormatInt(retryAfterSeconds(now, decision.ResetAt), 10))
				response.WriteError(w, req, http.StatusTooManyRequests, rateLimitExceededMessage)
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

func rateLimitClientKey(req *http.Request, trustedProxies []netip.Prefix) string {
	if req == nil {
		return ""
	}

	if userID := strings.TrimSpace(req.Header.Get(HeaderUserID)); userID != "" {
		return "user:" + userID
	}

	clientIP, ok := requestClientIP(req, trustedProxies)
	if !ok {
		return ""
	}

	return "ip:" + clientIP.String()
}

func requestClientIP(req *http.Request, trustedProxies []netip.Prefix) (netip.Addr, bool) {
	if req == nil {
		return netip.Addr{}, false
	}

	remoteIP, ok := parseRemoteIP(req.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}

	if trustedProxy(remoteIP, trustedProxies) {
		if forwardedIP, ok := parseForwardedFor(req.Header.Get("X-Forwarded-For")); ok {
			return forwardedIP, true
		}
	}

	return remoteIP, true
}

func parseRemoteIP(remoteAddr string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr)); err == nil {
		return parseIP(host)
	}

	return parseIP(remoteAddr)
}

func parseForwardedFor(value string) (netip.Addr, bool) {
	for _, part := range strings.Split(value, ",") {
		if addr, ok := parseIP(part); ok {
			return addr, true
		}
	}

	return netip.Addr{}, false
}

func parseIP(value string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, false
	}

	return addr.Unmap(), true
}

func trustedProxy(remoteIP netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(remoteIP) {
			return true
		}
	}

	return false
}

func setRateLimitHeaders(header http.Header, limit int, decision ratelimit.Decision) {
	header.Set(HeaderRateLimitLimit, strconv.Itoa(limit))
	header.Set(HeaderRateLimitRemaining, strconv.Itoa(decision.Remaining))
	header.Set(HeaderRateLimitReset, strconv.FormatInt(decision.ResetAt.Unix(), 10))
}

func retryAfterSeconds(now, resetAt time.Time) int64 {
	seconds := int64(math.Ceil(resetAt.Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}

	return seconds
}
