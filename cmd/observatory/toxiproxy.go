package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

const toxiproxyBaseURL = "http://toxiproxy:8474"

type ToxiproxyClient struct {
	httpClient *http.Client
	logger     *slog.Logger
	baseURL    string
}

func NewToxiproxyClient(httpClient *http.Client, logger *slog.Logger) *ToxiproxyClient {
	if logger == nil {
		logger = slog.Default()
	}

	return &ToxiproxyClient{
		httpClient: httpClient,
		logger:     logger,
		baseURL:    toxiproxyBaseURL,
	}
}

func (a *app) ensureToxiproxy(ctx context.Context) error {
	if err := a.toxiproxy.EnsureRedisProxy(ctx); err != nil {
		return err
	}
	a.toxiproxyReady = true
	return nil
}

func (c *ToxiproxyClient) EnsureRedisProxy(ctx context.Context) error {
	payload := []map[string]any{{
		"name":     "redis",
		"listen":   "0.0.0.0:6380",
		"upstream": "redis:6379",
		"enabled":  true,
	}}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal toxiproxy payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/populate", bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build toxiproxy ensure request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ensure toxiproxy redis proxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ensure toxiproxy redis proxy returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *ToxiproxyClient) RemoveAllToxics(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/proxies/redis", nil)
	if err != nil {
		return fmt.Errorf("build toxiproxy get proxy request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("get toxiproxy redis proxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("get toxiproxy redis proxy returned %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Toxics []struct {
			Name string `json:"name"`
		} `json:"toxics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode toxiproxy proxy response: %w", err)
	}

	for _, toxic := range payload.Toxics {
		deleteReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/proxies/redis/toxics/"+toxic.Name, nil)
		if err != nil {
			return fmt.Errorf("build toxiproxy delete toxic request: %w", err)
		}

		deleteResp, err := c.httpClient.Do(deleteReq)
		if err != nil {
			return fmt.Errorf("delete toxiproxy toxic %s: %w", toxic.Name, err)
		}
		deleteResp.Body.Close()
		if deleteResp.StatusCode >= http.StatusBadRequest && deleteResp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("delete toxiproxy toxic %s returned %d", toxic.Name, deleteResp.StatusCode)
		}
	}

	return nil
}
