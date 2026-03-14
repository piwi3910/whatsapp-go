package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")

	var req models.SendRequest

	if strings.HasPrefix(contentType, "application/json") || contentType == "" {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
	} else if strings.HasPrefix(contentType, "multipart/") {
		// Multipart form: inline upload+send
		if err := r.ParseMultipartForm(100 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
		req.To = r.FormValue("to")
		req.Type = r.FormValue("type")
		req.Content = r.FormValue("content")
		req.Caption = r.FormValue("caption")
		req.Filename = r.FormValue("filename")
		req.MediaID = r.FormValue("media_id")
		req.ContactJID = r.FormValue("contact_jid")
		req.Name = r.FormValue("name")
		if lat := r.FormValue("lat"); lat != "" {
			req.Lat, _ = strconv.ParseFloat(lat, 64)
		}
		if lon := r.FormValue("lon"); lon != "" {
			req.Lon, _ = strconv.ParseFloat(lon, 64)
		}
	} else {
		writeError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", "expected application/json or multipart/form-data")
		return
	}

	var resp *models.SendResponse
	var err error

	switch req.Type {
	case "text":
		resp, err = s.client.SendText(req.To, req.Content)
	case "image":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendImage(req.To, data, fname, req.Caption)
	case "video":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendVideo(req.To, data, fname, req.Caption)
	case "audio":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendAudio(req.To, data, fname)
	case "document":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendDocument(req.To, data, fname)
	case "sticker":
		data, _, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendSticker(req.To, data)
	case "location":
		resp, err = s.client.SendLocation(req.To, req.Lat, req.Lon, req.Name)
	case "contact":
		resp, err = s.client.SendContact(req.To, req.ContactJID)
	default:
		writeError(w, http.StatusBadRequest, "INVALID_TYPE", "unsupported message type: "+req.Type)
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getMediaData retrieves media bytes from either a media_id reference or multipart file upload.
func (s *Server) getMediaData(r *http.Request, req *models.SendRequest) ([]byte, string, error) {
	// Two-step: media_id reference
	if req.MediaID != "" {
		upload, err := s.store.GetMediaUpload(req.MediaID)
		if err != nil {
			return nil, "", err
		}
		s.store.DeleteMediaUpload(req.MediaID)
		fname := upload.Filename
		if fname == "" && req.Filename != "" {
			fname = req.Filename
		}
		return upload.Data, fname, nil
	}

	// Inline: multipart file
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}

	fname := header.Filename
	if req.Filename != "" {
		fname = req.Filename
	}
	return data, fname, nil
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	jid := r.URL.Query().Get("jid")
	if jid == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "jid query parameter is required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	var before int64
	if b := r.URL.Query().Get("before"); b != "" {
		if n, err := strconv.ParseInt(b, 10, 64); err == nil {
			before = n
		}
	}

	msgs, err := s.client.GetMessages(jid, limit, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}

	cursor := ""
	if len(msgs) > 0 {
		cursor = strconv.FormatInt(msgs[len(msgs)-1].Timestamp, 10)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"messages": msgs,
		"cursor":   cursor,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := s.client.GetMessage(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	forEveryone := r.URL.Query().Get("for_everyone") == "true"

	if err := s.client.DeleteMessage(id, forEveryone); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReactMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	if err := s.client.SendReaction(id, body.Emoji); err != nil {
		writeError(w, http.StatusInternalServerError, "REACTION_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.client.MarkRead(id); err != nil {
		writeError(w, http.StatusInternalServerError, "READ_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
