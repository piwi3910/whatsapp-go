package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.client.GetContacts(r.Context())
	if err != nil {
		writeServiceError(w, r, "contact.list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	// Validate up front so a malformed JID is a 400, not a 404 or a 500
	// raised somewhere inside the client.
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	contact, err := s.client.GetContactInfo(r.Context(), jid)
	if err != nil {
		writeServiceError(w, r, "contact.get", err)
		return
	}
	writeJSON(w, http.StatusOK, contact)
}

func (s *Server) handleBlockContact(w http.ResponseWriter, r *http.Request) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	if err := s.client.BlockContact(r.Context(), jid); err != nil {
		writeServiceError(w, r, "contact.block", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnblockContact(w http.ResponseWriter, r *http.Request) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	if err := s.client.UnblockContact(r.Context(), jid); err != nil {
		writeServiceError(w, r, "contact.unblock", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
