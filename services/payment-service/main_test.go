package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hitesh07082002/irongate/services/common"
)

func TestPaymentServiceRoutesRespond(t *testing.T) {
	payments := []map[string]any{
		{"id": "p-1", "order_id": "o-1", "status": "confirmed", "amount": 1299},
		{"id": "p-2", "order_id": "o-2", "status": "pending", "amount": 2599},
	}
	handlers := common.NewCollectionHandlers(payments, func(item map[string]any) string { return item["id"].(string) }, http.StatusOK, func() map[string]any {
		return map[string]any{"id": "p-3", "order_id": "o-3", "status": "confirmed", "amount": 499}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /payments", handlers.Create)
	mux.HandleFunc("GET /payments/{id}", handlers.Get)
	mux.HandleFunc("GET /health", common.HealthHandler("payment-service"))

	createResp := httptest.NewRecorder()
	mux.ServeHTTP(createResp, httptest.NewRequest(http.MethodPost, "/payments", nil))
	if createResp.Code != http.StatusOK {
		t.Fatalf("expected POST /payments 200, got %d", createResp.Code)
	}
	var created map[string]any
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode payment create response: %v", err)
	}
	if created["id"] != "p-3" {
		t.Fatalf("expected created payment p-3, got %#v", created)
	}

	healthResp := httptest.NewRecorder()
	mux.ServeHTTP(healthResp, httptest.NewRequest(http.MethodGet, "/health", nil))
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected GET /health 200, got %d", healthResp.Code)
	}
}
