package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/store"
)

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")

	var req models.SendRequest

	if strings.HasPrefix(contentType, "application/json") || contentType == "" {
		if !readJSON(w, r, &req) {
			return
		}
	} else if strings.HasPrefix(contentType, "multipart/") {
		// Multipart form: inline upload+send. multipartMemory is the heap
		// budget for the parse, not the accepted size — the router's
		// MaxBytesHandler still caps the whole body.
		if err := r.ParseMultipartForm(multipartMemory); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidBody, err.Error())
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
			v, err := strconv.ParseFloat(lat, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_PARAM", "invalid lat value")
				return
			}
			req.Lat = v
		}
		if lon := r.FormValue("lon"); lon != "" {
			v, err := strconv.ParseFloat(lon, 64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_PARAM", "invalid lon value")
				return
			}
			req.Lon = v
		}
	} else {
		writeError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", "expected application/json or multipart/form-data")
		return
	}

	// Validate the destination here rather than letting the client layer
	// fail deep inside a send: a malformed JID is the caller's mistake (400)
	// and must never be reported as a server fault. Normalising also means
	// "+1234567890" and "1234567890@s.whatsapp.net" address the same chat on
	// both the write and the read path.
	to, ok := normalizeJIDParam(w, req.To)
	if !ok {
		return
	}
	req.To = to

	var resp *models.SendResponse
	var err error

	switch req.Type {
	case "text":
		resp, err = s.client.SendText(r.Context(), req.To, req.Content)
	case "image":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeMediaInputError(w, r, mediaErr)
			return
		}
		resp, err = s.client.SendImage(r.Context(), req.To, data, fname, req.Caption)
	case "video":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeMediaInputError(w, r, mediaErr)
			return
		}
		resp, err = s.client.SendVideo(r.Context(), req.To, data, fname, req.Caption)
	case "audio":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeMediaInputError(w, r, mediaErr)
			return
		}
		resp, err = s.client.SendAudio(r.Context(), req.To, data, fname)
	case "document":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeMediaInputError(w, r, mediaErr)
			return
		}
		resp, err = s.client.SendDocument(r.Context(), req.To, data, fname)
	case "sticker":
		data, _, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeMediaInputError(w, r, mediaErr)
			return
		}
		resp, err = s.client.SendSticker(r.Context(), req.To, data)
	case "location":
		resp, err = s.client.SendLocation(r.Context(), req.To, req.Lat, req.Lon, req.Name)
	case "contact":
		// The attached contact's JID is caller input too, so a bad one is a
		// 400 rather than a failure deep inside the send.
		contactJID, ok := normalizeJIDParam(w, req.ContactJID)
		if !ok {
			return
		}
		resp, err = s.client.SendContact(r.Context(), req.To, contactJID)
	default:
		writeError(w, http.StatusBadRequest, "INVALID_TYPE", "unsupported message type: "+req.Type)
		return
	}

	if err != nil {
		writeServiceError(w, r, "message.send", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeMediaInputError reports a failure to resolve the media the caller
// asked to send. An unknown or expired media_id is the caller addressing
// something that is gone (404) rather than sending a malformed request (400):
// the distinction tells a client whether re-uploading will help.
// errBadMediaInput marks a media failure caused by the request itself (a
// missing or unreadable file part) rather than by the server.
var errBadMediaInput = errors.New("invalid media input")

func writeMediaInputError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, codeNotFound, "media_id not found or expired")
		return
	}
	// A store failure while reading the upload is a server fault, not bad
	// input: classify it rather than reporting a 400 whose message is the
	// raw wrapped error (which would leak SQL/driver detail to the caller).
	if !errors.Is(err, errBadMediaInput) {
		writeServiceError(w, r, "message.media", err)
		return
	}
	writeError(w, http.StatusBadRequest, "MEDIA_ERROR", err.Error())
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

	// Inline: multipart file. These two failures are the caller's doing (no
	// file part, or a body that ended early), so they stay 400s; the
	// media_id path above returns store errors, which are classified.
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errBadMediaInput, err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errBadMediaInput, err)
	}

	fname := header.Filename
	if req.Filename != "" {
		fname = req.Filename
	}
	return data, fname, nil
}

// parseMessageCursor decodes the ?before query parameter.
//
// The cursor a page returns is "<timestamp>.<message id>" — a keyset
// position, because second-granularity timestamps are not unique and paging
// on the timestamp alone drops every message that shares the boundary
// second. A bare integer is still accepted for backwards compatibility and
// means "strictly older than this timestamp"; it carries the old
// page-boundary hazard, so callers should round-trip the returned cursor.
//
// Anything else is an error rather than a silent reset to the newest page.
func parseMessageCursor(raw string) (*store.MessageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	tsPart, idPart, hasID := strings.Cut(raw, ".")
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil || ts < 0 {
		return nil, errors.New(`before must be a cursor of the form "<timestamp>.<message id>" or a unix timestamp`)
	}
	if hasID && idPart == "" {
		return nil, errors.New("before cursor has an empty message ID")
	}
	if ts == 0 {
		return nil, nil
	}
	return &store.MessageCursor{Timestamp: ts, ID: idPart}, nil
}

// handleListMessages serves a chat's message history, newest first.
//
// Wire contract:
//
//	GET /api/v1/messages?jid=<chat>&before=<cursor>&limit=<n>
//
//	200 {"ok":true,"data":{
//	      "messages":[...],          // always an array, never null
//	      "cursor":"<ts>.<id>"       // echoes ?before when the page is empty
//	    }}
//
//	400 INVALID_CURSOR / INVALID_LIMIT — malformed ?before or ?limit.
//
// limit defaults to 50 and is clamped to 500. Feed the returned cursor back
// as ?before to walk backwards through history without skipping messages
// that share a one-second timestamp.
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	rawJID := r.URL.Query().Get("jid")
	if rawJID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "jid query parameter is required")
		return
	}

	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", err.Error())
		return
	}

	before := r.URL.Query().Get("before")
	cur, err := parseMessageCursor(before)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_CURSOR", err.Error())
		return
	}

	// Normalise before hitting the store. Rows are keyed by the canonical
	// JID the send path writes, so querying "+1234567890" against the raw
	// string returned an empty page forever while sending to it worked.
	chatJID, ok := normalizeJIDParam(w, rawJID)
	if !ok {
		return
	}

	// Straight to the store: the keyset cursor cannot be expressed through
	// the Service interface's timestamp-only before parameter, and the
	// client method is a pass-through to this same call.
	msgs, err := s.store.GetMessagesPage(chatJID, limit, cur)
	if err != nil {
		writeServiceError(w, r, "message.list", err)
		return
	}

	// Echo the caller's cursor on an empty page rather than "", which
	// would restart a cursor-storing consumer at the newest message.
	cursor := before
	if len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		cursor = strconv.FormatInt(last.Timestamp, 10) + "." + last.ID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": msgs,
		"cursor":   cursor,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := s.client.GetMessage(r.Context(), id)
	if err != nil {
		writeServiceError(w, r, "message.get", err)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	forEveryone := r.URL.Query().Get("for_everyone") == "true"

	// A missing message is a 404 here, not a 500: retrying a delete of
	// something that does not exist can never succeed.
	if err := s.client.DeleteMessage(r.Context(), id, forEveryone); err != nil {
		writeServiceError(w, r, "message.delete", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReactMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Emoji string `json:"emoji"`
	}
	if !readJSON(w, r, &body) {
		return
	}

	if err := s.client.SendReaction(r.Context(), id, body.Emoji); err != nil {
		writeServiceError(w, r, "message.react", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.client.MarkRead(r.Context(), id); err != nil {
		writeServiceError(w, r, "message.markread", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
