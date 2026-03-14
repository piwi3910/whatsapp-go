package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		Participants []string `json:"participants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	group, err := s.client.CreateGroup(body.Name, body.Participants)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "CREATE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.client.GetGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	group, err := s.client.GetGroupInfo(jid)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleLeaveGroup(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.LeaveGroup(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "LEAVE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetInviteLink(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	link, err := s.client.GetInviteLink(jid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INVITE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"invite_link": link})
}

func (s *Server) handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InviteLink string `json:"invite_link"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	groupJID, err := s.client.JoinGroup(body.InviteLink)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JOIN_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"group_jid": groupJID})
}

func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request, action func(string, []string) error) {
	jid := chi.URLParam(r, "jid")
	var body struct {
		JIDs []string `json:"jids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if err := action(jid, body.JIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "PARTICIPANT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAddParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.AddParticipants)
}
func (s *Server) handleRemoveParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.RemoveParticipants)
}
func (s *Server) handlePromoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.PromoteParticipants)
}
func (s *Server) handleDemoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.DemoteParticipants)
}
