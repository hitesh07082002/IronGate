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

	payments := []map[string]any{
		{"id": "p-1", "order_id": "o-1", "status": "confirmed", "amount": 1299},
		{"id": "p-2", "order_id": "o-2", "status": "pending", "amount": 2599},
	}
	handlers := common.NewCollectionHandlers(payments, func(item map[string]any) string { return item["id"].(string) }, http.StatusOK, func() map[string]any {
		return map[string]any{"id": "p-3", "order_id": "o-3", "status": "confirmed", "amount": 499}
	})

	chaos := common.NewChaosState()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payments", handlers.Create)
	mux.HandleFunc("GET /payments/{id}", handlers.Get)
	mux.HandleFunc("GET /health", common.HealthHandler("payment-service"))
	common.RegisterChaosHandlers(mux, chaos)

	port := servicePort(8083)
	server := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: chaos.Middleware(mux), ReadHeaderTimeout: 5 * time.Second}
	slog.Info("service started", "service", "payment-service", "port", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("service stopped", "service", "payment-service", "error", err)
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
