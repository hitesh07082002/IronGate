package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToxiproxyEnsureRedisProxy(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotPayload []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewToxiproxyClient(server.Client(), slog.Default())
	client.baseURL = server.URL

	if err := client.EnsureRedisProxy(context.Background()); err != nil {
		t.Fatalf("ensure redis proxy: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/populate" {
		t.Fatalf("expected /populate, got %s", gotPath)
	}
	if len(gotPayload) != 1 {
		t.Fatalf("expected 1 proxy entry, got %d: %#v", len(gotPayload), gotPayload)
	}
	if gotPayload[0]["name"] != "redis" {
		t.Fatalf("expected redis proxy payload, got %#v", gotPayload)
	}
}

func TestToxiproxyRemoveAllToxicsDeletesEachToxic(t *testing.T) {
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies/redis":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"toxics":[{"name":"latency"},{"name":"timeout"}]}`)
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewToxiproxyClient(server.Client(), slog.Default())
	client.baseURL = server.URL

	if err := client.RemoveAllToxics(context.Background()); err != nil {
		t.Fatalf("remove all toxics: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("expected 2 toxics deleted, got %d", len(deleted))
	}
}
