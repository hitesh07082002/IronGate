package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hitesh07082002/irongate/internal/config"
)

const slidingWindowStrategy = "sliding_window"

var redisKeySanitizer = strings.NewReplacer("{", "_", "}", "_")

const slidingWindowScript = `
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
local min_score = now_ms - window_ms

redis.call("ZREMRANGEBYSCORE", key, "-inf", min_score)
local count = redis.call("ZCARD", key)

if count < limit then
  redis.call("ZADD", key, now_ms, member)
  redis.call("PEXPIRE", key, window_ms)
  local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
  local reset_at = now_ms + window_ms
  if oldest[2] ~= nil then
    reset_at = tonumber(oldest[2]) + window_ms
  end
  local remaining = limit - (count + 1)
  return {1, remaining, reset_at}
end

redis.call("PEXPIRE", key, window_ms)
local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
local reset_at = now_ms + window_ms
if oldest[2] ~= nil then
  reset_at = tonumber(oldest[2]) + window_ms
end

return {0, 0, reset_at}
`

type Store interface {
	Allow(ctx context.Context, request Request) (Decision, error)
}

type Request struct {
	Key      string
	Limit    int
	Window   time.Duration
	Strategy string
	Member   string
	Now      time.Time
}

type Decision struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

type RedisStore struct {
	client redis.UniversalClient
	script *redis.Script
}

func NewRedisStore(cfg config.RedisConfig) *RedisStore {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return &RedisStore{}
	}

	return NewRedisStoreWithClient(redis.NewClient(&redis.Options{
		Addr:     address,
		Password: cfg.Password,
		DB:       cfg.DB,
	}))
}

func NewRedisStoreWithClient(client redis.UniversalClient) *RedisStore {
	return &RedisStore{
		client: client,
		script: redis.NewScript(slidingWindowScript),
	}
}

func Key(clientKey, routePath string) string {
	return "rate_limit:{" + sanitizeRedisKeySegment(clientKey) + "}:" + routePath
}

func (s *RedisStore) Allow(ctx context.Context, request Request) (Decision, error) {
	if s == nil || s.client == nil {
		return Decision{}, errors.New("redis client is not configured")
	}
	if strings.TrimSpace(request.Key) == "" {
		return Decision{}, errors.New("rate limit key is required")
	}
	if strings.TrimSpace(request.Member) == "" {
		return Decision{}, errors.New("rate limit member is required")
	}
	if request.Window <= 0 {
		return Decision{}, errors.New("rate limit window must be greater than 0")
	}

	strategy := strings.TrimSpace(request.Strategy)
	if strategy == "" {
		strategy = slidingWindowStrategy
	}
	if strategy != slidingWindowStrategy {
		return Decision{}, fmt.Errorf("unsupported rate limit strategy %q", strategy)
	}

	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	values, err := s.script.Run(ctx, s.client, []string{request.Key},
		now.UnixMilli(),
		request.Window.Milliseconds(),
		request.Limit,
		request.Member,
	).Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(values) != 3 {
		return Decision{}, fmt.Errorf("unexpected rate limit script response length %d", len(values))
	}

	allowedFlag, err := redisInt(values[0])
	if err != nil {
		return Decision{}, fmt.Errorf("decode rate limit allowed flag: %w", err)
	}
	remaining, err := redisInt(values[1])
	if err != nil {
		return Decision{}, fmt.Errorf("decode rate limit remaining count: %w", err)
	}
	resetAtMillis, err := redisInt(values[2])
	if err != nil {
		return Decision{}, fmt.Errorf("decode rate limit reset time: %w", err)
	}

	return Decision{
		Allowed:   allowedFlag == 1,
		Remaining: int(remaining),
		ResetAt:   time.UnixMilli(resetAtMillis),
	}, nil
}

func sanitizeRedisKeySegment(value string) string {
	return redisKeySanitizer.Replace(strings.TrimSpace(value))
}

func redisInt(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("unexpected redis integer string %q", typed)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected redis integer type %T", value)
	}
}
