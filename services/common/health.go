package common

import "net/http"

func HealthHandler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": service,
		})
	}
}
