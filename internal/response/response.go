package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ErrorBody struct {
	Error     string `json:"error"`
	Code      int    `json:"code"`
	RequestID string `json:"request_id"`
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

func WriteError(w http.ResponseWriter, req *http.Request, status int, message string) {
	WriteJSON(w, status, ErrorBody{
		Error:     message,
		Code:      status,
		RequestID: RequestID(req),
	})
}

func RequestID(req *http.Request) string {
	if req == nil {
		return ""
	}

	return req.Header.Get("X-Request-ID")
}
