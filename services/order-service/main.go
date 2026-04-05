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

	orders := []map[string]any{
		{"id": "o-1", "user_id": "u-1", "status": "created", "total": 1299},
		{"id": "o-2", "user_id": "u-2", "status": "shipped", "total": 2599},
		{"id": "o-3", "user_id": "u-3", "status": "delivered", "total": 499},
	}
	handlers := common.NewCollectionHandlers(orders, func(item map[string]any) string { return item["id"].(string) }, http.StatusCreated, func() map[string]any {
		return map[string]any{"id": "o-4", "user_id": "u-1", "status": "created", "total": 1899}
	})

	chaos := common.NewChaosState()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", handlers.List)
	mux.HandleFunc("GET /orders/{id}", handlers.Get)
	mux.HandleFunc("POST /orders", handlers.Create)
	mux.HandleFunc("GET /health", common.HealthHandler("order-service"))
	common.RegisterChaosHandlers(mux, chaos)

	port := servicePort(8082)
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: chaos.Middleware(mux), ReadHeaderTimeout: 5 * time.Second}
	slog.Info("service started", "service", "order-service", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("service stopped", "service", "order-service", "error", err)
		os.Exit(1)
	}
}

func servicePort(defaultPort int) int {
	portValue := os.Getenv("PORT")
	if portValue == "" {
		return defaultPort
	}

	port, err := strconv.Atoi(portValue)
	if err != nil || port <= 0 {
		slog.Warn("invalid PORT value, using default", "value", portValue, "default", defaultPort)
		return defaultPort
	}

	return port
}
