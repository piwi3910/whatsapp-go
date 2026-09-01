package api

import (
	"encoding/json"
	"net/http"

	"github.com/piwi3910/whatsapp-go/internal/jid"
)

// handleHistorySync backfills past messages of a chat from the user's
// primary device (issue #21). The server holds the persistent connection,
// so it is the only place that can keep receiving the sync while the
// request waits.
func (s *Server) handleHistorySync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatJID string `json:"chat_jid"`
		Count   int    `json:"count"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if body.ChatJID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_CHAT_JID", "chat_jid is required")
		return
	}
	if _, err := jid.NormalizeJID(body.ChatJID); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JID", err.Error())
		return
	}

	// The wait window inside SyncHistory is bounded, but a slow primary
	// device can hold this handler for over a minute; give it room and let
	// client cancellation still cut it off.
	imported, err := s.client.SyncHistory(r.Context(), body.ChatJID, body.Count)
	if err != nil {
		writeServiceError(w, r, "history sync", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported})
}
