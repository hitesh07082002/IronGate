package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/hitesh07082002/irongate/services/common"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

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
	mux.HandleFunc("POST /users/login", func(w http.ResponseWriter, _ *http.Request) {
		common.WriteJSON(w, http.StatusOK, map[string]string{
			"token":   "placeholder",
			"message": "real JWT signing added in Phase 3",
		})
	})
	mux.HandleFunc("POST /users/register", func(w http.ResponseWriter, _ *http.Request) {
		common.WriteJSON(w, http.StatusCreated, map[string]any{
			"id":      "u-5",
			"email":   "registered@example.com",
			"message": "account created",
		})
	})
	mux.HandleFunc("GET /health", common.HealthHandler("user-service"))
	common.RegisterChaosHandlers(mux, chaos)

	port := common.ServicePortFromEnv(8081)
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: chaos.Middleware(mux), ReadHeaderTimeout: 5 * time.Second}
	slog.Info("service started", "service", "user-service", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("service stopped", "service", "user-service", "error", err)
		os.Exit(1)
	}
}
