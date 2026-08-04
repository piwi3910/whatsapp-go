package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"go.mau.fi/whatsmeow"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// Stable error codes. These are part of the wire contract: a client branches
// on the code, not on the message, so they must not change meaning. The
// message is human-facing and deliberately says nothing about the internals
// that produced it.
const (
	codeInvalidJID   = "INVALID_JID"
	codeNotFound     = "NOT_FOUND"
	codeUnavailable  = "UNAVAILABLE"
	codeConflict     = "CONFLICT"
	codeInternal     = "INTERNAL_ERROR"
	codeInvalidBody  = "INVALID_BODY"
	codeBodyTooLarge = "BODY_TOO_LARGE"
	codeRateLimited  = "RATE_LIMITED"
)

// fallbackErrorBody is written when the real body could not be marshalled.
// Pre-rendered so that path cannot itself fail.
var fallbackErrorBody = []byte(`{"ok":false,"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`)

func writeJSON(w http.ResponseWriter, status int, data any) {
	writeEnvelope(w, status, models.APIResponse{OK: true, Data: data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeEnvelope(w, status, models.APIResponse{
		OK:    false,
		Error: &models.APIError{Code: code, Message: message},
	})
}

// writeEnvelope renders the {ok,data} / {ok,error} envelope.
//
// The body is marshalled BEFORE the status line is committed. Encoding
// straight into the ResponseWriter (the previous behaviour) writes the 200
// first, so a value that fails to marshal half way through left the caller
// with a success status and a truncated body — which a lenient parser can
// still read as a partial success. Marshalling first means such a failure is
// still reportable as a 500 with a complete, well-formed body.
func writeEnvelope(w http.ResponseWriter, status int, env models.APIResponse) {
	body, err := json.Marshal(env)
	if err != nil {
		slog.Error("marshalling response body", "error", err, "intended_status", status)
		body, status = fallbackErrorBody, http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	// Belt and braces: these bodies are JSON, but a browser that sniffs an
	// attacker-influenced string field as HTML would execute it.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeServiceError maps an error from the WhatsApp client or the store onto
// the HTTP status a caller can actually act on, and logs the real error
// server-side.
//
// The old behaviour — 500 with err.Error() — was unusable twice over. A
// client could not tell "never retry" (bad JID, unknown message) from "retry
// shortly" (session reconnecting), and the raw error text leaked SQL
// fragments, file paths and internal identifiers to whoever called the API.
func writeServiceError(w http.ResponseWriter, r *http.Request, op string, err error) {
	status, code, message := classifyError(err)
	logf := slog.Warn
	if status >= 500 {
		// Only genuine faults deserve an error-level line: a 404 or a
		// disconnected session is normal operation and must not pollute an
		// error-rate alert.
		logf = slog.Error
	}
	logf("request failed",
		"op", op,
		"request_id", RequestIDFromContext(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"code", code,
		"error", err,
	)
	if status == http.StatusServiceUnavailable {
		// Tells a well-behaved client this is worth retrying, and roughly
		// when, instead of leaving it to guess a backoff.
		w.Header().Set("Retry-After", "5")
	}
	writeError(w, status, code, message)
}

// classifyError is the single place that decides retryability. Keep the
// buckets meaningful:
//
//	400 the request is wrong and will stay wrong — never retry
//	404 the addressed resource does not exist — never retry as-is
//	409 the operation conflicts with current state — retry once resolved
//	503 the dependency is down or slow — retry with backoff
//	500 we have a bug — retrying is unlikely to help and an operator must look
func classifyError(err error) (status int, code, message string) {
	var disconnected *whatsmeow.DisconnectedError
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, codeNotFound, "resource not found"

	case errors.Is(err, whatsapp.ErrNotLoggedIn), errors.Is(err, whatsmeow.ErrNotLoggedIn):
		// Not 401: the *caller* is authenticated; it is this server's
		// WhatsApp session that is unusable. That is a dependency outage and
		// it resolves without the caller changing anything.
		return http.StatusServiceUnavailable, codeUnavailable, "whatsapp session is not logged in"

	case errors.Is(err, whatsmeow.ErrNotConnected),
		errors.Is(err, whatsmeow.ErrIQTimedOut),
		errors.Is(err, whatsmeow.ErrMessageTimedOut),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled),
		errors.As(err, &disconnected):
		return http.StatusServiceUnavailable, codeUnavailable, "whatsapp is temporarily unavailable, retry shortly"

	case errors.Is(err, whatsapp.ErrAlreadyLoggedIn), errors.Is(err, whatsapp.ErrLoginInProgress):
		// Our own sentinels, so the text is ours and safe to echo.
		return http.StatusConflict, codeConflict, err.Error()
	}
	return http.StatusInternalServerError, codeInternal, "internal server error"
}

// normalizeJIDParam validates and canonicalises a caller-supplied JID,
// answering 400 when it is not one. Returns false once it has written the
// error response.
//
// Read paths used to pass the raw string straight to the store while write
// paths normalised it, so sending to "+1234567890" worked but querying the
// same chat returned an empty page forever — the stored rows are keyed by the
// canonical "1234567890@s.whatsapp.net". Normalising on both sides is what
// makes the two halves of the API agree.
func normalizeJIDParam(w http.ResponseWriter, raw string) (string, bool) {
	normalized, err := jid.NormalizeJID(raw)
	if err != nil {
		// The jid package's messages describe the caller's own input
		// ("phone number %q must be 5-15 digits") and leak nothing internal.
		writeError(w, http.StatusBadRequest, codeInvalidJID, err.Error())
		return "", false
	}
	return normalized, true
}
