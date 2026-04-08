package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDashboardEndpointsBatchPrometheusReads(t *testing.T) {
	app := newTestApp(t)
	var (
		mu     sync.Mutex
		counts = map[string]int{}
	)
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		counts[req.URL.Path]++
		mu.Unlock()

		query := req.URL.Query().Get("query")
		resultType := "vector"
		result := `[{"metric":{},"value":[1712550000,"1"]}]`
		if strings.Contains(req.URL.Path, "query_range") {
			resultType = "matrix"
			result = `[{"metric":{},"values":[[1712550000,"1"],[1712550010,"2"]]}]`
		}
		body := `{"status":"success","data":{"resultType":"` + resultType + `","result":` + result + `}}`
		if strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("expected query in request URL, got %q", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	app.globalLimiter = NewIPRateLimiter(1000, time.Minute)
	handler := app.routes()

	request := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	landing := request("/api/dashboard/landing")
	if landing.Code != http.StatusOK {
		t.Fatalf("landing dashboard status = %d, want %d", landing.Code, http.StatusOK)
	}
	landingPayload := decodeJSONResponse[landingDashboardResponse](t, landing)
	if len(landingPayload.InFlight) == 0 || len(landingPayload.TotalRPS) == 0 || len(landingPayload.RateLimited) == 0 {
		t.Fatalf("unexpected landing dashboard payload: %#v", landingPayload)
	}
	mu.Lock()
	landingQueryCount := counts["/api/v1/query"]
	landingRangeCount := counts["/api/v1/query_range"]
	counts = map[string]int{}
	mu.Unlock()
	if landingQueryCount != 6 || landingRangeCount != 0 {
		t.Fatalf("landing dashboard Prometheus reads = query:%d query_range:%d, want 6 and 0", landingQueryCount, landingRangeCount)
	}

	chaos := request("/api/dashboard/chaos")
	if chaos.Code != http.StatusOK {
		t.Fatalf("chaos dashboard status = %d, want %d", chaos.Code, http.StatusOK)
	}
	chaosPayload := decodeJSONResponse[chaosDashboardResponse](t, chaos)
	if len(chaosPayload.RequestRate) == 0 || len(chaosPayload.LatencyP95) == 0 || len(chaosPayload.RetryCount) == 0 {
		t.Fatalf("unexpected chaos dashboard payload: %#v", chaosPayload)
	}
	mu.Lock()
	chaosQueryCount := counts["/api/v1/query"]
	chaosRangeCount := counts["/api/v1/query_range"]
	mu.Unlock()
	if chaosQueryCount != 4 || chaosRangeCount != 7 {
		t.Fatalf("chaos dashboard Prometheus reads = query:%d query_range:%d, want 4 and 7", chaosQueryCount, chaosRangeCount)
	}
}
