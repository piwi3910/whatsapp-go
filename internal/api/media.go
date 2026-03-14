package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FILE", "file field is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "READ_ERROR", err.Error())
		return
	}

	// Generate media ID
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	mediaID := "med_" + hex.EncodeToString(idBytes)

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(data)
	}

	upload := &models.MediaUpload{
		ID:        mediaID,
		MimeType:  mime,
		Filename:  header.Filename,
		Size:      int64(len(data)),
		Data:      data,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}

	if err := s.store.InsertMediaUpload(upload); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"media_id": mediaID,
		"type":     mime,
		"mime":     mime,
		"size":     len(data),
	})
}

func (s *Server) handleDownloadMedia(w http.ResponseWriter, r *http.Request) {
	messageID := chi.URLParam(r, "messageId")
	data, mimeType, err := s.client.DownloadMedia(messageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
