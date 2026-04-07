package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hitesh07082002/irongate/services/common"
)

func TestOrderServiceRoutesRespond(t *testing.T) {
	orders := []map[string]any{
		{"id": "o-1", "user_id": "u-1", "status": "created", "total": 1299},
		{"id": "o-2", "user_id": "u-2", "status": "shipped", "total": 2599},
		{"id": "o-3", "user_id": "u-3", "status": "delivered", "total": 499},
	}
	handlers := common.NewCollectionHandlers(orders, func(item map[string]any) string { return item["id"].(string) }, http.StatusCreated, func() map[string]any {
		return map[string]any{"id": "o-4", "user_id": "u-1", "status": "created", "total": 1899}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", handlers.List)
	mux.HandleFunc("GET /orders/{id}", handlers.Get)
	mux.HandleFunc("POST /orders", handlers.Create)
	mux.HandleFunc("GET /health", common.HealthHandler("order-service"))

	listResp := httptest.NewRecorder()
	mux.ServeHTTP(listResp, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected GET /orders 200, got %d", listResp.Code)
	}
	var listed []map[string]any
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode orders list: %v", err)
	}
	if len(listed) != len(orders) {
		t.Fatalf("expected %d orders, got %d", len(orders), len(listed))
	}

	getResp := httptest.NewRecorder()
	mux.ServeHTTP(getResp, httptest.NewRequest(http.MethodGet, "/orders/o-1", nil))
	if getResp.Code != http.StatusOK {
		t.Fatalf("expected GET /orders/{id} 200, got %d", getResp.Code)
	}

	createResp := httptest.NewRecorder()
	mux.ServeHTTP(createResp, httptest.NewRequest(http.MethodPost, "/orders", nil))
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected POST /orders 201, got %d", createResp.Code)
	}

	healthResp := httptest.NewRecorder()
	mux.ServeHTTP(healthResp, httptest.NewRequest(http.MethodGet, "/health", nil))
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected GET /health 200, got %d", healthResp.Code)
	}
}
