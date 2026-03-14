package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	whID := "wh_" + hex.EncodeToString(idBytes)

	wh := &models.Webhook{
		ID:     whID,
		URL:    body.URL,
		Events: body.Events,
		Secret: body.Secret,
	}

	if err := s.store.InsertWebhook(wh); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Register with dispatcher
	s.dispatcher.Register(*wh)

	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.store.GetWebhooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": webhooks})
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.DeleteWebhook(id); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	s.dispatcher.Unregister(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
