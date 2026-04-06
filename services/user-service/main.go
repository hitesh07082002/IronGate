package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hitesh07082002/irongate/services/common"
)

const loginTokenTTL = time.Hour

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	jwtSecret, err := requiredJWTSecret(os.Getenv("JWT_SECRET"))
	if err != nil {
		slog.Error("failed to start user-service", "error", err)
		os.Exit(1)
	}

	port := common.ServicePortFromEnv(8081)
	server := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           newHandler(jwtSecret),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("service started", "service", "user-service", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("service stopped", "service", "user-service", "error", err)
		os.Exit(1)
	}
}

func newHandler(jwtSecret string) http.Handler {
	users := []map[string]any{
		{"id": "u-1", "name": "Ada Lovelace", "email": "ada@example.com", "role": "admin"},
		{"id": "u-2", "name": "Grace Hopper", "email": "grace@example.com", "role": "user"},
		{"id": "u-3", "name": "Katherine Johnson", "email": "katherine@example.com", "role": "user"},
	}
	handlers := common.NewCollectionHandlers(users, func(item map[string]any) string { return item["id"].(string) }, http.StatusCreated, func() map[string]any {
		return map[string]any{"id": "u-4", "name": "New User", "email": "new.user@example.com", "role": "user"}
	})

	chaos := common.NewChaosState()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users", handlers.List)
	mux.HandleFunc("GET /users/{id}", handlers.Get)
	mux.HandleFunc("POST /users", handlers.Create)
	mux.HandleFunc("POST /users/login", loginHandler(jwtSecret, users[0]))
	mux.HandleFunc("POST /users/register", func(w http.ResponseWriter, _ *http.Request) {
		common.WriteJSON(w, http.StatusCreated, map[string]any{
			"id":      "u-5",
			"email":   "registered@example.com",
			"message": "account created",
		})
	})
	mux.HandleFunc("GET /health", common.HealthHandler("user-service"))
	common.RegisterChaosHandlers(mux, chaos)

	return chaos.Middleware(mux)
}

func loginHandler(jwtSecret string, user map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(jwtSecret) == "" {
			writeLoginError(w, r, http.StatusInternalServerError, "jwt secret not configured")
			return
		}

		subject, role, err := resolveLoginClaims(r, user)
		if err != nil {
			writeLoginError(w, r, http.StatusBadRequest, "invalid login request")
			return
		}

		token, err := signLoginToken(jwtSecret, subject, role, time.Now())
		if err != nil {
			slog.Error("failed to sign login token", "error", err)
			writeLoginError(w, r, http.StatusInternalServerError, "failed to sign token")
			return
		}

		common.WriteJSON(w, http.StatusOK, map[string]string{
			"token": token,
		})
	}
}

func resolveLoginClaims(r *http.Request, user map[string]any) (string, string, error) {
	subject, err := requiredUserClaim(user, "id")
	if err != nil {
		return "", "", err
	}
	role, err := requiredUserClaim(user, "role")
	if err != nil {
		return "", "", err
	}
	if r == nil || r.Body == nil {
		return subject, role, nil
	}

	var payload struct {
		Subject string `json:"subject"`
		Role    string `json:"role"`
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			return subject, role, nil
		}

		return "", "", err
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return "", "", fmt.Errorf("unexpected trailing JSON payload")
		}

		return "", "", err
	}

	overrideSubject := strings.TrimSpace(payload.Subject)
	overrideRole := strings.TrimSpace(payload.Role)
	if (overrideSubject != "" || overrideRole != "") && !loginOverridesEnabled() {
		return "", "", fmt.Errorf("login overrides are disabled")
	}

	if overrideSubject != "" {
		subject = overrideSubject
	}
	if overrideRole != "" {
		role = overrideRole
	}

	return subject, role, nil
}

func requiredUserClaim(user map[string]any, key string) (string, error) {
	raw, ok := user[key]
	if !ok {
		return "", fmt.Errorf("%s missing", key)
	}

	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must be set", key)
	}

	return value, nil
}

func loginOverridesEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("IRONGATE_ALLOW_LOGIN_OVERRIDES")), "true")
}

func writeLoginError(w http.ResponseWriter, r *http.Request, status int, message string) {
	common.WriteJSON(w, status, map[string]any{
		"error":      message,
		"code":       status,
		"request_id": requestID(r),
	})
}

func requestID(r *http.Request) string {
	if r == nil {
		return ""
	}

	return strings.TrimSpace(r.Header.Get("X-Request-ID"))
}

func signLoginToken(jwtSecret, subject, role string, issuedAt time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub":  subject,
		"role": role,
		"iat":  issuedAt.Unix(),
		"exp":  issuedAt.Add(loginTokenTTL).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecret))
}

func requiredJWTSecret(raw string) (string, error) {
	jwtSecret := strings.TrimSpace(raw)
	if jwtSecret == "" {
		return "", fmt.Errorf("JWT_SECRET must be set")
	}

	return jwtSecret, nil
}
