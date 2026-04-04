package common

import (
	"encoding/json"
	"net/http"
)

type CollectionHandlers struct {
	List   http.HandlerFunc
	Get    http.HandlerFunc
	Create http.HandlerFunc
}

func NewCollectionHandlers[T any](items []T, idFn func(T) string, createStatus int, createFn func() T) CollectionHandlers {
	itemsCopy := append([]T(nil), items...)
	index := make(map[string]T, len(itemsCopy))
	for _, item := range itemsCopy {
		index[idFn(item)] = item
	}

	return CollectionHandlers{
		List: func(w http.ResponseWriter, _ *http.Request) {
			WriteJSON(w, http.StatusOK, itemsCopy)
		},
		Get: func(w http.ResponseWriter, r *http.Request) {
			item, ok := index[r.PathValue("id")]
			if !ok {
				WriteJSON(w, http.StatusNotFound, map[string]any{
					"error": "resource not found",
				})
				return
			}
			WriteJSON(w, http.StatusOK, item)
		},
		Create: func(w http.ResponseWriter, _ *http.Request) {
			WriteJSON(w, createStatus, createFn())
		},
	}
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
