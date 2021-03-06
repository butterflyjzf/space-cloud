package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/spaceuptech/space-cloud/gateway/model"
)

// HandleCluster returns handler cluster
func HandleCluster() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get the JWT token from header
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(model.Response{Error: "not implemented in open source"})
	}
}
