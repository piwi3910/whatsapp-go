package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
)

// webhookView is the API representation of a webhook.
//
// It exists so a caller can tell the two kinds apart. Webhooks created
// through this API live in the database and are fully mutable. Webhooks
// declared in the server's configuration file are registered at boot, are
// not in the database, and cannot be created or deleted here — the config
// file is their source of truth, and "deleting" one would silently come back
// on the next restart. Marking them explicitly is what turns a confusing 404
// on DELETE into an answerable "this one is config-managed".
type webhookView struct {
	models.Webhook
	Source webhook.Source `json:"source"`
	// ConfigManaged is redundant with Source but spelled out because it is
	// the property clients actually branch on (hide the delete button).
	ConfigManaged bool `json:"config_managed"`
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret,omitempty"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	// Reject unusable or dangerous targets before storing, so the caller
	// learns immediately instead of registering a webhook that will only
	// ever fail at dial time.
	if err := s.dispatcher.Policy().ValidateURL(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_URL", err.Error())
		return
	}

	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "ID_ERROR", "generating webhook id")
		return
	}
	// "wh_" + 32 hex chars: structurally incapable of colliding with the
	// config namespace (see webhook.ConfigIDPrefix).
	whID := "wh_" + hex.EncodeToString(idBytes)

	wh := &models.Webhook{
		ID:     whID,
		URL:    body.URL,
		Events: body.Events,
		Secret: body.Secret,
	}

	if err := s.store.InsertWebhook(wh); err != nil {
		writeServiceError(w, r, "webhook.create", err)
		return
	}

	// Register with dispatcher. This can only fail if the dispatcher is
	// shutting down; say so rather than reporting a success the caller will
	// never see deliveries from.
	if err := s.dispatcher.Register(*wh); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DISPATCHER_UNAVAILABLE", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	stored, err := s.store.GetWebhooks()
	if err != nil {
		writeServiceError(w, r, "webhook.list", err)
		return
	}

	views := make([]webhookView, 0, len(stored))
	for _, wh := range stored {
		views = append(views, webhookView{Webhook: wh, Source: webhook.SourceAPI})
	}
	// Config webhooks are never in the database, so they have to come from
	// the dispatcher — otherwise an operator looking at GET /webhooks cannot
	// see the endpoints the server is actually delivering to.
	for _, reg := range s.dispatcher.List() {
		if reg.Source != webhook.SourceConfig {
			continue
		}
		wh := reg.Webhook
		// The secret comes from a file on the server; it is not something an
		// API caller supplied, so do not hand it back out.
		wh.Secret = ""
		views = append(views, webhookView{Webhook: wh, Source: webhook.SourceConfig, ConfigManaged: true})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })

	writeJSON(w, http.StatusOK, map[string]any{"webhooks": views})
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Check the namespace before the store: a config webhook is not in the
	// database, so hitting the store first reports "not found", which is
	// true but useless — the webhook plainly exists and is delivering.
	if webhook.IsConfigManaged(id) {
		writeError(w, http.StatusConflict, "CONFIG_MANAGED",
			"webhook "+id+" is declared in the server configuration file; "+
				"remove it there and restart the server. It cannot be deleted through the API.")
		return
	}

	if err := s.store.DeleteWebhook(id); err != nil {
		writeServiceError(w, r, "webhook.delete", err)
		return
	}
	s.dispatcher.Unregister(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
