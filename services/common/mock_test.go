package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewCollectionHandlersCreatePersistsForListAndGet(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	handlers := NewCollectionHandlers(
		[]item{{ID: "existing"}},
		func(value item) string { return value.ID },
		http.StatusCreated,
		func() item { return item{ID: "created"} },
	)

	createReq := httptest.NewRequest(http.MethodPost, "/items", nil)
	createResp := httptest.NewRecorder()
	handlers.Create(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d", http.StatusCreated, createResp.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/items", nil)
	listResp := httptest.NewRecorder()
	handlers.List(listResp, listReq)

	var listed []item
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2 items after create, got %d", len(listed))
	}
	if listed[1].ID != "created" {
		t.Fatalf("expected created item to be persisted, got %#v", listed[1])
	}

	getReq := httptest.NewRequest(http.MethodGet, "/items/created", nil)
	getReq.SetPathValue("id", "created")
	getResp := httptest.NewRecorder()
	handlers.Get(getResp, getReq)

	var got item
	if err := json.Unmarshal(getResp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.ID != "created" {
		t.Fatalf("expected created item on get, got %#v", got)
	}
}
