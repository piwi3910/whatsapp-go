package api

import (
	"net/http"
	"strconv"
)

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	if a := r.URL.Query().Get("after"); a != "" {
		if n, err := strconv.ParseInt(a, 10, 64); err == nil {
			after = n
		}
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := s.store.GetEvents(after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}

	cursor := ""
	if len(events) > 0 {
		cursor = strconv.FormatInt(events[len(events)-1].ID, 10)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"cursor": cursor,
	})
}
