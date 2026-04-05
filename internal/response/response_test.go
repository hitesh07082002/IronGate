package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONSetsHeadersAndBody(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteJSON(recorder, http.StatusCreated, map[string]string{"status": "ok"})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestWriteErrorIncludesRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req.Header.Set("X-Request-ID", "spoofed-request-id")
	req.Header.Del("X-Request-ID")

	generatedRequestID := "generated-req-123"
	req.Header.Set("X-Request-ID", generatedRequestID)

	recorder := httptest.NewRecorder()
	WriteError(recorder, req, http.StatusBadGateway, "upstream failed")

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var payload ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Error != "upstream failed" || payload.Code != http.StatusBadGateway || payload.RequestID != generatedRequestID {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
}

func TestRequestIDReturnsEmptyWithoutGeneratedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req.Header.Set("X-Request-ID", "spoofed-request-id")
	req.Header.Del("X-Request-ID")

	if got := RequestID(req); got != "" {
		t.Fatalf("expected empty request id without generated header, got %q", got)
	}
}

func TestRequestIDHandlesNilRequest(t *testing.T) {
	if got := RequestID(nil); got != "" {
		t.Fatalf("expected empty request id for nil request, got %q", got)
	}
}
