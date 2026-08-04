package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		Participants []string `json:"participants"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// Every participant is validated before the call so one bad entry is a
	// 400 naming the offender, rather than a 500 from deep inside the client.
	participants, ok := normalizeJIDList(w, body.Participants)
	if !ok {
		return
	}
	group, err := s.client.CreateGroup(r.Context(), body.Name, participants)
	if err != nil {
		writeServiceError(w, r, "group.create", err)
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.client.GetGroups(r.Context())
	if err != nil {
		writeServiceError(w, r, "group.list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	group, err := s.client.GetGroupInfo(r.Context(), jid)
	if err != nil {
		writeServiceError(w, r, "group.get", err)
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleLeaveGroup(w http.ResponseWriter, r *http.Request) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	if err := s.client.LeaveGroup(r.Context(), jid); err != nil {
		writeServiceError(w, r, "group.leave", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetInviteLink(w http.ResponseWriter, r *http.Request) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	link, err := s.client.GetInviteLink(r.Context(), jid)
	if err != nil {
		writeServiceError(w, r, "group.invite_link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"invite_link": link})
}

func (s *Server) handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InviteLink string `json:"invite_link"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.InviteLink == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "invite_link is required")
		return
	}
	groupJID, err := s.client.JoinGroup(r.Context(), body.InviteLink)
	if err != nil {
		writeServiceError(w, r, "group.join", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"group_jid": groupJID})
}

// handleParticipants is the shared body of the four participant mutations.
// op names the operation for the error log so a 500 can be traced back to
// which one failed.
func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request, op string, action func(context.Context, string, []string) error) {
	jid, ok := normalizeJIDParam(w, chi.URLParam(r, "jid"))
	if !ok {
		return
	}
	var body struct {
		JIDs []string `json:"jids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	jids, ok := normalizeJIDList(w, body.JIDs)
	if !ok {
		return
	}
	if len(jids) == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "jids must contain at least one JID")
		return
	}
	if err := action(r.Context(), jid, jids); err != nil {
		writeServiceError(w, r, op, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// normalizeJIDList validates every JID in a request body, answering 400 on
// the first bad one. Returns false once it has written the error response.
func normalizeJIDList(w http.ResponseWriter, raw []string) ([]string, bool) {
	if raw == nil {
		return nil, true
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		normalized, ok := normalizeJIDParam(w, v)
		if !ok {
			return nil, false
		}
		out = append(out, normalized)
	}
	return out, true
}

func (s *Server) handleAddParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, "group.participants.add", s.client.AddParticipants)
}
func (s *Server) handleRemoveParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, "group.participants.remove", s.client.RemoveParticipants)
}
func (s *Server) handlePromoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, "group.participants.promote", s.client.PromoteParticipants)
}
func (s *Server) handleDemoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, "group.participants.demote", s.client.DemoteParticipants)
}
