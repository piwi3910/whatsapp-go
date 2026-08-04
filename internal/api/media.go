package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// multipartMemory is the in-memory portion of a multipart parse, NOT the
// total accepted size (the router's MaxBytesHandler caps that). Parts larger
// than this spill to a temp file, so a 100MB upload no longer needs 100MB of
// heap merely to be parsed.
const multipartMemory = 8 << 20

// maxPreallocUpload bounds the buffer readAllSized will allocate from a
// caller-declared length, so a lying part header cannot make us reserve
// arbitrary memory up front.
const maxPreallocUpload = 128 << 20

func (s *Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FILE", "file field is required")
		return
	}
	defer file.Close()

	// MEMORY: the upload exists in full in this []byte and again as a bound
	// BLOB parameter on the way to SQLite, so peak RSS is roughly 2x the
	// largest accepted upload per concurrent upload. Size a container's
	// memory limit accordingly — with the default max_upload_size of 100MB,
	// budget ~200MB per concurrent upload on top of the baseline, or lower
	// max_upload_size.
	data, err := readAllSized(file, header.Size)
	if err != nil {
		writeServiceError(w, r, "media.upload.read", err)
		return
	}

	// Generate media ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		// A predictable media ID would let one caller fetch or consume
		// another's pending upload, so a failure here must fail the request
		// rather than fall back to something guessable.
		writeServiceError(w, r, "media.upload.id", err)
		return
	}
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
		writeServiceError(w, r, "media.upload.store", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"media_id": mediaID,
		"type":     mime,
		"mime":     mime,
		"size":     len(data),
	})
}

// readAllSized reads r into a buffer sized from the multipart part's declared
// length. io.ReadAll grows and copies repeatedly, peaking at ~2x the final
// size; one correctly sized allocation removes that transient. The declared
// size is a hint, not a guarantee, so a short or long body is still handled
// correctly.
func readAllSized(r io.Reader, size int64) ([]byte, error) {
	if size <= 0 || size > maxPreallocUpload {
		return io.ReadAll(r)
	}
	buf := make([]byte, size)
	n, err := io.ReadFull(r, buf)
	switch {
	case err == nil:
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		// Shorter than declared: keep what arrived.
		return buf[:n], nil
	default:
		return nil, err
	}
	// Longer than declared: drain the remainder rather than truncating.
	rest, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return append(buf, rest...), nil
}

// safeDownloadMIMEs is the allowlist of Content-Type values this server will
// reflect on a media download. Everything else becomes
// application/octet-stream.
//
// The stored media_type is the SENDER's declared mimetype — fully
// attacker-controlled. Echoing it let a sender serve "text/html" from this
// origin, i.e. stored XSS against any browser-rendered console or dashboard
// that links to the endpoint. Only types WhatsApp actually carries as media,
// and that no browser treats as active content, are listed.
var safeDownloadMIMEs = map[string]bool{
	"image/jpeg":               true,
	"image/png":                true,
	"image/gif":                true,
	"image/webp":               true,
	"image/bmp":                true,
	"image/tiff":               true,
	"video/mp4":                true,
	"video/3gpp":               true,
	"video/quicktime":          true,
	"video/webm":               true,
	"video/x-matroska":         true,
	"audio/mpeg":               true,
	"audio/mp4":                true,
	"audio/aac":                true,
	"audio/amr":                true,
	"audio/ogg":                true,
	"audio/wav":                true,
	"audio/x-wav":              true,
	"audio/webm":               true,
	"application/pdf":          true,
	"application/zip":          true,
	"application/octet-stream": true,
}

// sanitizeMediaMIME reduces a sender-declared mimetype to a safe one.
// Parameters (";charset=...") are dropped: they are another injection point
// and carry no meaning for the binary types above.
func sanitizeMediaMIME(mime string) string {
	base, _, _ := strings.Cut(mime, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	if safeDownloadMIMEs[base] {
		return base
	}
	return "application/octet-stream"
}

func (s *Server) handleDownloadMedia(w http.ResponseWriter, r *http.Request) {
	messageID := chi.URLParam(r, "messageId")
	data, mimeType, err := s.client.DownloadMedia(r.Context(), messageID)
	if err != nil {
		// An unknown message is a 404, but a failed decrypt or a dead socket
		// is not — the caller must be able to tell "gone" from "retry".
		writeServiceError(w, r, "media.download", err)
		return
	}

	w.Header().Set("Content-Type", sanitizeMediaMIME(mimeType))
	// nosniff stops a browser from ignoring the sanitised type and sniffing
	// the bytes as HTML anyway; attachment stops it rendering the response as
	// a document in this origin at all. Both are needed — either alone leaves
	// a bypass.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
