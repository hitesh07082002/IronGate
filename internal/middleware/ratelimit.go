package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/response"
)

const (
	HeaderRateLimitLimit     = "X-RateLimit-Limit"
	HeaderRateLimitRemaining = "X-RateLimit-Remaining"
	HeaderRateLimitReset     = "X-RateLimit-Reset"
	HeaderRetryAfter         = "Retry-After"

	rateLimitExceededMessage            = "rate limit exceeded"
	rateLimitConfigInvalidMessage       = "rate limit configuration invalid"
	rateLimitMetadataMissingMessage     = "rate limit metadata missing"
	rateLimitStrategyUnsupportedMessage = "route rate limit strategy is not implemented yet"
	defaultRateLimitStrategy            = "sliding_window"
	rateLimitStoreTimeout               = 250 * time.Millisecond
	rateLimitBucketHashBytes            = 6
)

type RateLimiterOptions struct {
	TrustedProxies []netip.Prefix
}

func RateLimiter(store ratelimit.Store, logger *slog.Logger, options RateLimiterOptions) Middleware {
	return RateLimiterWithMetrics(store, logger, nil, options)
}

func RateLimiterWithMetrics(store ratelimit.Store, logger *slog.Logger, registry *gatewaymetrics.Registry, options RateLimiterOptions) Middleware {
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
			if route.RateLimit.Requests <= 0 || route.RateLimit.Window <= 0 {
				logger.Error("rate limit configuration invalid; rejecting request",
					"path", req.URL.Path,
					"route", route.Path,
					"request_id", response.RequestID(req),
					"requests", route.RateLimit.Requests,
					"window", route.RateLimit.Window,
				)
				response.WriteError(w, req, http.StatusInternalServerError, rateLimitConfigInvalidMessage)
				return
			}

			clientKey := rateLimitClientKey(req, options.TrustedProxies)
			requestID := strings.TrimSpace(req.Header.Get(HeaderRequestID))
			if clientKey == "" || requestID == "" {
				response.WriteError(w, req, http.StatusInternalServerError, rateLimitMetadataMissingMessage)
				return
			}

			if store == nil {
				bucketKind, bucketKeyHash := rateLimitBucketLogFields(clientKey)
				logger.Warn("rate limit store unavailable; allowing request",
					"path", req.URL.Path,
					"route", route.Path,
					"request_id", response.RequestID(req),
					"bucket_kind", bucketKind,
					"bucket_key_hash", bucketKeyHash,
					"reason", "store not configured",
				)
				next.ServeHTTP(w, req)
				return
			}

			now := time.Now()
			storeCtx, cancel := context.WithTimeout(req.Context(), rateLimitStoreTimeout)
			decision, err := store.Allow(storeCtx, ratelimit.Request{
				Key:      ratelimit.Key(clientKey, route.Path),
				Limit:    route.RateLimit.Requests,
				Window:   route.RateLimit.Window,
				Strategy: strategy,
				Member:   requestID,
				Now:      now,
			})
			cancel()
			if err != nil {
				bucketKind, bucketKeyHash := rateLimitBucketLogFields(clientKey)
				logger.Warn("rate limit store unavailable; allowing request",
					"path", req.URL.Path,
					"route", route.Path,
					"request_id", response.RequestID(req),
					"bucket_kind", bucketKind,
					"bucket_key_hash", bucketKeyHash,
					"error", err,
				)
				next.ServeHTTP(w, req)
				return
			}

			setRateLimitHeaders(w.Header(), route.RateLimit.Requests, decision)
			if !decision.Allowed {
				registry.IncRateLimitRejection(route.Service)
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
		if forwardedChain, ok := parseForwardedForChain(req.Header.Get("X-Forwarded-For")); ok {
			return forwardedClientIP(remoteIP, forwardedChain, trustedProxies), true
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

type forwardedHop struct {
	addr  netip.Addr
	valid bool
}

func parseForwardedForChain(value string) ([]forwardedHop, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}

	parts := strings.Split(value, ",")
	forwarded := make([]forwardedHop, 0, len(parts))
	for _, part := range parts {
		addr, ok := parseIP(part)
		forwarded = append(forwarded, forwardedHop{
			addr:  addr,
			valid: ok,
		})
	}

	return forwarded, len(forwarded) > 0
}

// Walk from the trusted edge toward the client and stop once the chain becomes unverifiable.
func forwardedClientIP(remoteIP netip.Addr, forwarded []forwardedHop, trustedProxies []netip.Prefix) netip.Addr {
	downstreamHop := remoteIP
	for index := len(forwarded) - 1; index >= 0; index-- {
		if !trustedProxy(downstreamHop, trustedProxies) {
			return downstreamHop
		}

		candidate := forwarded[index]
		if !candidate.valid {
			return downstreamHop
		}
		if !trustedProxy(candidate.addr, trustedProxies) {
			return candidate.addr
		}
		downstreamHop = candidate.addr
	}

	return downstreamHop
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

func rateLimitBucketLogFields(clientKey string) (string, string) {
	bucketKind, _, ok := strings.Cut(clientKey, ":")
	if !ok || strings.TrimSpace(bucketKind) == "" {
		bucketKind = "unknown"
	}

	sum := sha256.Sum256([]byte(clientKey))
	return bucketKind, hex.EncodeToString(sum[:rateLimitBucketHashBytes])
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
