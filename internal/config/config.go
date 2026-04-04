package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const gatewayInternalService = "gateway-internal"

var (
	validLoadBalancers = map[string]struct{}{
		"round_robin": {},
		"weighted":    {},
		"least_conn":  {},
	}
	validRateLimitStrategies = map[string]struct{}{
		"sliding_window": {},
		"token_bucket":   {},
	}
)

type Config struct {
	Server         ServerConfig  `yaml:"server"`
	Routes         []RouteConfig `yaml:"routes"`
	CircuitBreaker CBConfig      `yaml:"circuit_breaker"`
	Auth           AuthConfig    `yaml:"auth"`
	Redis          RedisConfig   `yaml:"redis"`
	Metrics        MetricsConfig `yaml:"metrics"`
	Logging        LoggingConfig `yaml:"logging"`
}

type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type RouteConfig struct {
	Path         string           `yaml:"path"`
	StripPrefix  string           `yaml:"strip_prefix"`
	Service      string           `yaml:"service"`
	Methods      []string         `yaml:"methods"`
	AuthRequired bool             `yaml:"auth_required"`
	Timeout      time.Duration    `yaml:"timeout"`
	RateLimit    *RateLimitConfig `yaml:"rate_limit"`
	Retry        RetryConfig      `yaml:"retry"`
	Targets      []Target         `yaml:"targets"`
	LoadBalancer string           `yaml:"load_balancer"`
}

type Target struct {
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
	Weight int    `yaml:"weight"`
}

type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BaseDelay   time.Duration `yaml:"base_delay"`
	MaxDelay    time.Duration `yaml:"max_delay"`
	Jitter      string        `yaml:"jitter"`
}

type RateLimitConfig struct {
	Requests int           `yaml:"requests"`
	Window   time.Duration `yaml:"window"`
	Strategy string        `yaml:"strategy"`
}

type CBConfig struct {
	FailureThreshold    int           `yaml:"failure_threshold"`
	SuccessThreshold    int           `yaml:"success_threshold"`
	Timeout             time.Duration `yaml:"timeout"`
	WindowSize          time.Duration `yaml:"window_size"`
	HalfOpenMaxRequests int           `yaml:"half_open_max_requests"`
}

type AuthConfig struct {
	JWTSecret    string `yaml:"jwt_secret"`
	JWTAlgorithm string `yaml:"jwt_algorithm"`
}

type RedisConfig struct {
	Address  string `yaml:"address"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func Load(path string) (*Config, error) {
	rawConfig, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expandedConfig := os.ExpandEnv(string(rawConfig))
	decoder := yaml.NewDecoder(strings.NewReader(expandedConfig))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Validate() []error {
	var errs []error

	if c.Server.Port <= 0 {
		errs = append(errs, errors.New("server.port must be greater than 0"))
	}
	if c.Server.ReadTimeout <= 0 {
		errs = append(errs, errors.New("server.read_timeout must be greater than 0"))
	}
	if c.Server.WriteTimeout <= 0 {
		errs = append(errs, errors.New("server.write_timeout must be greater than 0"))
	}
	if c.CircuitBreaker.FailureThreshold < 0 {
		errs = append(errs, errors.New("circuit_breaker.failure_threshold must not be negative"))
	}
	if c.CircuitBreaker.SuccessThreshold < 0 {
		errs = append(errs, errors.New("circuit_breaker.success_threshold must not be negative"))
	}
	if c.CircuitBreaker.Timeout < 0 {
		errs = append(errs, errors.New("circuit_breaker.timeout must not be negative"))
	}
	if c.CircuitBreaker.WindowSize < 0 {
		errs = append(errs, errors.New("circuit_breaker.window_size must not be negative"))
	}
	if c.CircuitBreaker.HalfOpenMaxRequests < 0 {
		errs = append(errs, errors.New("circuit_breaker.half_open_max_requests must not be negative"))
	}

	authRequired := false
	for index := range c.Routes {
		route := &c.Routes[index]
		routeName := fmt.Sprintf("routes[%d] (%q)", index, route.Path)

		if strings.TrimSpace(route.Path) == "" {
			errs = append(errs, fmt.Errorf("%s path must not be empty", routeName))
		}
		if strings.TrimSpace(route.Service) == "" {
			errs = append(errs, fmt.Errorf("%s service must not be empty", routeName))
		}
		if route.Timeout < 0 {
			errs = append(errs, fmt.Errorf("%s timeout must not be negative", routeName))
		}

		if route.AuthRequired {
			authRequired = true
		}

		if route.RateLimit != nil {
			if route.RateLimit.Requests < 0 {
				errs = append(errs, fmt.Errorf("%s rate_limit.requests must not be negative", routeName))
			}
			if route.RateLimit.Window < 0 {
				errs = append(errs, fmt.Errorf("%s rate_limit.window must not be negative", routeName))
			}
			if route.RateLimit.Strategy != "" {
				if _, ok := validRateLimitStrategies[route.RateLimit.Strategy]; !ok {
					errs = append(errs, fmt.Errorf("%s rate_limit.strategy %q is invalid", routeName, route.RateLimit.Strategy))
				}
			}
		}

		if route.Retry.MaxAttempts < 0 {
			errs = append(errs, fmt.Errorf("%s retry.max_attempts must not be negative", routeName))
		}
		if route.Retry.BaseDelay < 0 {
			errs = append(errs, fmt.Errorf("%s retry.base_delay must not be negative", routeName))
		}
		if route.Retry.MaxDelay < 0 {
			errs = append(errs, fmt.Errorf("%s retry.max_delay must not be negative", routeName))
		}

		if route.Service == gatewayInternalService {
			continue
		}

		if len(route.Targets) == 0 {
			errs = append(errs, fmt.Errorf("%s must define at least one target", routeName))
		}

		if _, ok := validLoadBalancers[route.LoadBalancer]; !ok {
			errs = append(errs, fmt.Errorf("%s load_balancer %q is invalid", routeName, route.LoadBalancer))
		}

		for targetIndex, target := range route.Targets {
			targetName := fmt.Sprintf("%s targets[%d]", routeName, targetIndex)
			if strings.TrimSpace(target.Host) == "" {
				errs = append(errs, fmt.Errorf("%s host must not be empty", targetName))
			}
			if target.Port <= 0 {
				errs = append(errs, fmt.Errorf("%s port must be greater than 0", targetName))
			}
			if target.Weight < 0 {
				errs = append(errs, fmt.Errorf("%s weight must not be negative", targetName))
			}
		}
	}

	if authRequired && strings.TrimSpace(c.Auth.JWTSecret) == "" {
		errs = append(errs, errors.New("auth.jwt_secret is required when any route has auth_required: true"))
	}

	return errs
}
