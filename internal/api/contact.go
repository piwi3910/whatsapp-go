package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.client.GetContacts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	contact, err := s.client.GetContactInfo(jid)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, contact)
}

func (s *Server) handleBlockContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.BlockContact(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "BLOCK_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnblockContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.UnblockContact(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "UNBLOCK_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
