package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// stubService implements whatsapp.Service by embedding the interface (so the
// dozens of methods no test touches need no boilerplate) and overriding only
// the handful each test exercises. A call to an un-stubbed method panics
// loudly rather than silently returning a zero value, which keeps a test from
// passing for the wrong reason.
type stubService struct {
	whatsapp.Service

	state         string
	sendText      func(ctx context.Context, jid, text string) (*models.SendResponse, error)
	sendImage     func(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	getMessage    func(ctx context.Context, id string) (*models.Message, error)
	deleteMessage func(ctx context.Context, id string, forEveryone bool) error
	sendReaction  func(ctx context.Context, id, emoji string) error
	markRead      func(ctx context.Context, id string) error
	download      func(ctx context.Context, id string) ([]byte, string, error)
	contactInfo   func(ctx context.Context, jid string) (*models.Contact, error)
}

func (s *stubService) Status() whatsapp.ConnectionStatus {
	return whatsapp.ConnectionStatus{State: s.state}
}

func (s *stubService) SendText(ctx context.Context, jid, text string) (*models.SendResponse, error) {
	return s.sendText(ctx, jid, text)
}

func (s *stubService) SendImage(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return s.sendImage(ctx, jid, data, filename, caption)
}

func (s *stubService) GetMessage(ctx context.Context, id string) (*models.Message, error) {
	return s.getMessage(ctx, id)
}

func (s *stubService) DeleteMessage(ctx context.Context, id string, forEveryone bool) error {
	return s.deleteMessage(ctx, id, forEveryone)
}

func (s *stubService) SendReaction(ctx context.Context, id, emoji string) error {
	return s.sendReaction(ctx, id, emoji)
}

func (s *stubService) MarkRead(ctx context.Context, id string) error {
	return s.markRead(ctx, id)
}

func (s *stubService) DownloadMedia(ctx context.Context, id string) ([]byte, string, error) {
	return s.download(ctx, id)
}

func (s *stubService) GetContactInfo(ctx context.Context, jid string) (*models.Contact, error) {
	return s.contactInfo(ctx, jid)
}

const testAPIKey = "test-key"

// testServer builds a server with a real store and a routed chi router, so
// tests exercise the same middleware chain (request id, recoverer, auth,
// body limits) that production requests go through.
func testServer(t *testing.T, svc whatsapp.Service) *Server {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewServer(svc, st, nil, testAPIKey, "test", 100<<20)
}

func authRequest(method, path string, body []byte) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+testAPIKey)
	return r
}

func do(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)
	return rec
}

// ---------------------------------------------------------------------------
// Two-step media flow: the upload is deleted only after a successful send,
// so a failed send leaves the file in place for a retry (issue #8).
// ---------------------------------------------------------------------------

func TestTwoStepMediaDeletedOnlyAfterSuccessfulSend(t *testing.T) {
	seedUpload := func(s *Server, id string) {
		t.Helper()
		if err := s.store.InsertMediaUpload(&models.MediaUpload{
			ID: id, MimeType: "image/png", Filename: "x.png",
			Size: 2, Data: []byte("ok"),
			CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatalf("seeding upload: %v", err)
		}
	}

	t.Run("successful send consumes the upload", func(t *testing.T) {
		stub := &stubService{}
		stub.sendImage = func(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
			if string(data) != "ok" {
				t.Fatalf("send got %q, want the uploaded bytes", data)
			}
			return &models.SendResponse{MessageID: "m1", Timestamp: time.Now().Unix()}, nil
		}
		s := testServer(t, stub)
		seedUpload(s, "med_1")

		rec := do(s, authRequest("POST", "/api/v1/messages/send",
			[]byte(`{"to":"1234567890@s.whatsapp.net","type":"image","media_id":"med_1"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if _, err := s.store.GetMediaUpload("med_1"); err != store.ErrNotFound {
			t.Fatalf("upload should be deleted after successful send, got err=%v", err)
		}
	})

	t.Run("failed send keeps the upload for retry", func(t *testing.T) {
		stub := &stubService{}
		stub.sendImage = func(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
			return nil, fmt.Errorf("device not connected")
		}
		s := testServer(t, stub)
		seedUpload(s, "med_2")

		rec := do(s, authRequest("POST", "/api/v1/messages/send",
			[]byte(`{"to":"1234567890@s.whatsapp.net","type":"image","media_id":"med_2"}`)))
		if rec.Code == http.StatusOK {
			t.Fatalf("expected a failure, got 200")
		}
		if _, err := s.store.GetMediaUpload("med_2"); err != nil {
			t.Fatalf("upload must survive a failed send, got err=%v", err)
		}
	})
}

// ---------------------------------------------------------------------------

// The point of the mapping is that a client can decide whether to retry, so
// each bucket is asserted directly on its sentinel, including through the
// fmt.Errorf wrapping the client layer adds.
func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"store not found", store.ErrNotFound, http.StatusNotFound, codeNotFound},
		{"wrapped store not found", fmt.Errorf("message not found: %w", store.ErrNotFound), http.StatusNotFound, codeNotFound},
		{"sql no rows", sql.ErrNoRows, http.StatusNotFound, codeNotFound},
		{"library not logged in", whatsapp.ErrNotLoggedIn, http.StatusServiceUnavailable, codeUnavailable},
		{"whatsmeow not logged in", whatsmeow.ErrNotLoggedIn, http.StatusServiceUnavailable, codeUnavailable},
		{"not connected", fmt.Errorf("sending text: %w", whatsmeow.ErrNotConnected), http.StatusServiceUnavailable, codeUnavailable},
		{"iq timeout", whatsmeow.ErrIQTimedOut, http.StatusServiceUnavailable, codeUnavailable},
		{"send timeout", whatsmeow.ErrMessageTimedOut, http.StatusServiceUnavailable, codeUnavailable},
		{"deadline", context.DeadlineExceeded, http.StatusServiceUnavailable, codeUnavailable},
		{"disconnected", &whatsmeow.DisconnectedError{Action: "test"}, http.StatusServiceUnavailable, codeUnavailable},
		{"already logged in", whatsapp.ErrAlreadyLoggedIn, http.StatusConflict, codeConflict},
		{"login in progress", whatsapp.ErrLoginInProgress, http.StatusConflict, codeConflict},
		{"unknown", fmt.Errorf("some internal explosion"), http.StatusInternalServerError, codeInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, code, _ := classifyError(tc.err)
			if status != tc.want || code != tc.code {
				t.Errorf("classifyError(%v) = %d/%s, want %d/%s", tc.err, status, code, tc.want, tc.code)
			}
		})
	}
}

// A raw err.Error() in the body hands the caller SQL text, file paths and
// internal identifiers.
func TestInternalErrorDoesNotLeakDetail(t *testing.T) {
	secret := `no such table: messages in /var/lib/wa/store.db`
	svc := &stubService{getMessage: func(context.Context, string) (*models.Message, error) {
		return nil, fmt.Errorf("querying: %s", secret)
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodGet, "/api/v1/messages/abc", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "no such table") || strings.Contains(rec.Body.String(), "/var/lib") {
		t.Errorf("response leaked internal detail: %s", rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env.OK || env.Error == nil || env.Error.Code != codeInternal {
		t.Errorf("body = %s, want an INTERNAL_ERROR envelope", rec.Body.String())
	}
}

// Every handler that reaches the WhatsApp client must map, not blanket-500.
func TestHandlerErrorStatuses(t *testing.T) {
	notFound := fmt.Errorf("message not found: %w", store.ErrNotFound)
	offline := fmt.Errorf("sending: %w", whatsmeow.ErrNotConnected)

	tests := []struct {
		name    string
		svc     *stubService
		request func() *http.Request
		want    int
		code    string
	}{
		{
			name: "send to invalid jid is 400",
			svc:  &stubService{},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/send", []byte(`{"to":"not a jid","type":"text","content":"hi"}`))
			},
			want: http.StatusBadRequest, code: codeInvalidJID,
		},
		{
			name: "send while logged out is 503",
			svc: &stubService{sendText: func(context.Context, string, string) (*models.SendResponse, error) {
				return nil, whatsapp.ErrNotLoggedIn
			}},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/send", []byte(`{"to":"12345678901","type":"text","content":"hi"}`))
			},
			want: http.StatusServiceUnavailable, code: codeUnavailable,
		},
		{
			name: "send while disconnected is 503",
			svc: &stubService{sendText: func(context.Context, string, string) (*models.SendResponse, error) {
				return nil, offline
			}},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/send", []byte(`{"to":"12345678901","type":"text","content":"hi"}`))
			},
			want: http.StatusServiceUnavailable, code: codeUnavailable,
		},
		{
			name: "get missing message is 404",
			svc: &stubService{getMessage: func(context.Context, string) (*models.Message, error) {
				return nil, notFound
			}},
			request: func() *http.Request {
				return authRequest(http.MethodGet, "/api/v1/messages/nope", nil)
			},
			want: http.StatusNotFound, code: codeNotFound,
		},
		{
			name: "delete missing message is 404",
			svc:  &stubService{deleteMessage: func(context.Context, string, bool) error { return notFound }},
			request: func() *http.Request {
				return authRequest(http.MethodDelete, "/api/v1/messages/nope", nil)
			},
			want: http.StatusNotFound, code: codeNotFound,
		},
		{
			name: "react to missing message is 404",
			svc:  &stubService{sendReaction: func(context.Context, string, string) error { return notFound }},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/nope/react", []byte(`{"emoji":"👍"}`))
			},
			want: http.StatusNotFound, code: codeNotFound,
		},
		{
			name: "markread on missing message is 404",
			svc:  &stubService{markRead: func(context.Context, string) error { return notFound }},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/nope/read", nil)
			},
			want: http.StatusNotFound, code: codeNotFound,
		},
		{
			name: "markread while disconnected is 503",
			svc:  &stubService{markRead: func(context.Context, string) error { return offline }},
			request: func() *http.Request {
				return authRequest(http.MethodPost, "/api/v1/messages/nope/read", nil)
			},
			want: http.StatusServiceUnavailable, code: codeUnavailable,
		},
		{
			name: "contact with malformed jid is 400",
			svc:  &stubService{},
			request: func() *http.Request {
				return authRequest(http.MethodGet, "/api/v1/contacts/bogus", nil)
			},
			want: http.StatusBadRequest, code: codeInvalidJID,
		},
		{
			name: "download of unknown message is 404",
			svc: &stubService{download: func(context.Context, string) ([]byte, string, error) {
				return nil, "", notFound
			}},
			request: func() *http.Request {
				return authRequest(http.MethodGet, "/api/v1/media/nope", nil)
			},
			want: http.StatusNotFound, code: codeNotFound,
		},
		{
			name: "download while disconnected is 503",
			svc: &stubService{download: func(context.Context, string) ([]byte, string, error) {
				return nil, "", offline
			}},
			request: func() *http.Request {
				return authRequest(http.MethodGet, "/api/v1/media/x", nil)
			},
			want: http.StatusServiceUnavailable, code: codeUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(testServer(t, tc.svc), tc.request())
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			env := decodeEnvelope(t, rec)
			if env.OK {
				t.Errorf("ok = true on an error response: %s", rec.Body.String())
			}
			if env.Error == nil || env.Error.Code != tc.code {
				t.Errorf("error = %+v, want code %s", env.Error, tc.code)
			}
			// A retryable answer must say so, or a client has to guess.
			if tc.want == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
				t.Error("503 response has no Retry-After header")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M8 — JID normalization on read paths
// ---------------------------------------------------------------------------

// Sending to "+1234567890" stores rows under "1234567890@s.whatsapp.net", so
// a read path that does not normalise finds nothing for the same address the
// caller just successfully sent to.
func TestListMessages_NormalizesJID(t *testing.T) {
	s := pagingServer(t)
	const canonical = "12345678901@s.whatsapp.net"
	seedMessages(t, s, canonical, 3, 1)

	for _, form := range []string{canonical, "12345678901", "+12345678901"} {
		rec := getMessages(t, s, "jid="+strings.ReplaceAll(form, "+", "%2B"))
		if rec.Code != http.StatusOK {
			t.Fatalf("jid=%s: status = %d (%s)", form, rec.Code, rec.Body.String())
		}
		msgs, _ := decodeEnvelope(t, rec).Data["messages"].([]any)
		if len(msgs) != 3 {
			t.Errorf("jid=%s returned %d messages, want 3", form, len(msgs))
		}
	}
}

func TestListMessages_InvalidJIDIs400(t *testing.T) {
	s := pagingServer(t)
	for _, bad := range []string{"nonsense", "123@evil.example", "12345678901@", "@s.whatsapp.net"} {
		rec := getMessages(t, s, "jid="+bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("jid=%s: status = %d, want 400 (%s)", bad, rec.Code, rec.Body.String())
			continue
		}
		if env := decodeEnvelope(t, rec); env.Error == nil || env.Error.Code != codeInvalidJID {
			t.Errorf("jid=%s: body = %s, want %s", bad, rec.Body.String(), codeInvalidJID)
		}
	}
}

// The send path must accept the same flexible forms the read path now does.
func TestSendMessage_NormalizesDestination(t *testing.T) {
	var got string
	svc := &stubService{sendText: func(_ context.Context, jid, _ string) (*models.SendResponse, error) {
		got = jid
		return &models.SendResponse{MessageID: "m1"}, nil
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodPost, "/api/v1/messages/send",
		[]byte(`{"to":"+12345678901","type":"text","content":"hi"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got != "12345678901@s.whatsapp.net" {
		t.Errorf("client received to=%q, want the canonical JID", got)
	}
}

// ---------------------------------------------------------------------------
// M10 — media download content type
// ---------------------------------------------------------------------------

func TestSanitizeMediaMIME(t *testing.T) {
	tests := map[string]string{
		"image/png":                "image/png",
		"IMAGE/PNG":                "image/png",
		"image/jpeg; charset=utf8": "image/jpeg",
		"  video/mp4  ":            "video/mp4",
		// The dangerous cases: a sender chooses this string.
		"text/html":                       "application/octet-stream",
		"image/svg+xml":                   "application/octet-stream",
		"application/xhtml+xml":           "application/octet-stream",
		"text/html; charset=utf-8":        "application/octet-stream",
		"application/javascript":          "application/octet-stream",
		"":                                "application/octet-stream",
		"image/png\r\nX-Injected: yes":    "application/octet-stream",
		"multipart/x-mixed-replace;b=xyz": "application/octet-stream",
	}
	for in, want := range tests {
		if got := sanitizeMediaMIME(in); got != want {
			t.Errorf("sanitizeMediaMIME(%q) = %q, want %q", in, got, want)
		}
	}
}

// A sender-declared text/html turns this endpoint into stored XSS against
// anything that renders it in a browser.
func TestDownloadMedia_HardensResponse(t *testing.T) {
	payload := []byte(`<script>alert(document.domain)</script>`)
	svc := &stubService{download: func(context.Context, string) ([]byte, string, error) {
		return payload, "text/html", nil
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodGet, "/api/v1/media/msg1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want it neutralised to application/octet-stream", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Error("body was altered; only the headers should change")
	}
}

// Legitimate media must still be served with its real type, or every client
// has to sniff.
func TestDownloadMedia_KeepsSafeContentType(t *testing.T) {
	svc := &stubService{download: func(context.Context, string) ([]byte, string, error) {
		return []byte{0x89, 'P', 'N', 'G'}, "image/png", nil
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodGet, "/api/v1/media/msg1", nil))
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

// ---------------------------------------------------------------------------
// M15 — body limits and rate limiting
// ---------------------------------------------------------------------------

// The router's 100MB MaxBytesHandler applies to JSON routes too, so without a
// per-decode limit a 100MB body to /messages/send is buffered in full.
func TestJSONBodyIsLimited(t *testing.T) {
	body := []byte(`{"to":"12345678901","type":"text","content":"` + strings.Repeat("A", maxJSONBody+1024) + `"}`)
	svc := &stubService{sendText: func(context.Context, string, string) (*models.SendResponse, error) {
		t.Error("handler reached the client with an over-sized body")
		return &models.SendResponse{}, nil
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodPost, "/api/v1/messages/send", body))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", rec.Code, rec.Body.String())
	}
	if env := decodeEnvelope(t, rec); env.Error == nil || env.Error.Code != codeBodyTooLarge {
		t.Errorf("body = %s, want %s", rec.Body.String(), codeBodyTooLarge)
	}
}

func TestJSONBodyUnderLimitIsAccepted(t *testing.T) {
	body := []byte(`{"to":"12345678901","type":"text","content":"` + strings.Repeat("A", 4096) + `"}`)
	svc := &stubService{sendText: func(context.Context, string, string) (*models.SendResponse, error) {
		return &models.SendResponse{MessageID: "m1"}, nil
	}}
	rec := do(testServer(t, svc), authRequest(http.MethodPost, "/api/v1/messages/send", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

// The JSON limit must not bleed into media upload, which is the one route
// that legitimately carries megabytes.
func TestMediaUploadKeepsLargeLimit(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	payload := bytes.Repeat([]byte("x"), 4*maxJSONBody)
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", &buf)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec := do(testServer(t, &stubService{}), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := decodeEnvelope(t, rec).Data["size"]; got != float64(len(payload)) {
		t.Errorf("stored size = %v, want %d", got, len(payload))
	}
}

func TestRateLimiterTokenBucket(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(2, 3) // 2/s sustained, burst of 3
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d rejected inside the burst", i)
		}
	}
	if l.allow("a") {
		t.Error("burst was not enforced: a 4th immediate request was allowed")
	}
	// A different client has its own bucket.
	if !l.allow("b") {
		t.Error("an unrelated client was throttled by another client's usage")
	}
	// Half a second at 2/s earns exactly one token.
	now = now.Add(500 * time.Millisecond)
	if !l.allow("a") {
		t.Error("no token accrued after 500ms at 2/s")
	}
	if l.allow("a") {
		t.Error("more than one token accrued in 500ms at 2/s")
	}
}

func TestRateLimiterEvictsIdleBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(1, 1)
	l.now = func() time.Time { return now }

	l.allow("stale")
	now = now.Add(rateLimitIdleTTL + rateLimitSweepEvery)
	l.allow("fresh")

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.buckets["stale"]; ok {
		t.Error("idle bucket was not evicted; the map grows without bound")
	}
	if _, ok := l.buckets["fresh"]; !ok {
		t.Error("active bucket was evicted")
	}
}

func TestRateLimitMiddlewareRejects(t *testing.T) {
	t.Setenv("WA_RATE_LIMIT_RPS", "1")
	t.Setenv("WA_RATE_LIMIT_BURST", "2")
	s := testServer(t, &stubService{getMessage: func(context.Context, string) (*models.Message, error) {
		return &models.Message{ID: "m"}, nil
	}})

	var limited *httptest.ResponseRecorder
	for i := 0; i < 5; i++ {
		rec := do(s, authRequest(http.MethodGet, "/api/v1/messages/m", nil))
		if rec.Code == http.StatusTooManyRequests {
			limited = rec
			break
		}
	}
	if limited == nil {
		t.Fatal("no request was rate limited with burst=2 over 5 requests")
	}
	if got := limited.Header().Get("Retry-After"); got == "" {
		t.Error("429 response has no Retry-After header")
	}
	if env := decodeEnvelope(t, limited); env.Error == nil || env.Error.Code != codeRateLimited {
		t.Errorf("body = %s, want %s", limited.Body.String(), codeRateLimited)
	}
}

// Probes must never be throttled: a kubelet that gets a 429 restarts a
// perfectly healthy pod.
func TestRateLimitSkipsProbes(t *testing.T) {
	t.Setenv("WA_RATE_LIMIT_RPS", "1")
	t.Setenv("WA_RATE_LIMIT_BURST", "1")
	s := testServer(t, &stubService{state: stateConnected})
	for i := 0; i < 10; i++ {
		rec := do(s, httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("probe %d = %d, want 200", i, rec.Code)
		}
	}
}

func TestRateLimitCanBeDisabled(t *testing.T) {
	t.Setenv("WA_RATE_LIMIT_RPS", "0")
	if l := rateLimiterFromEnv(); l != nil {
		t.Fatal("WA_RATE_LIMIT_RPS=0 must disable the limiter")
	}
	s := testServer(t, &stubService{getMessage: func(context.Context, string) (*models.Message, error) {
		return &models.Message{ID: "m"}, nil
	}})
	for i := 0; i < 100; i++ {
		if rec := do(s, authRequest(http.MethodGet, "/api/v1/messages/m", nil)); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d with limiting disabled (%s)", i, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Response envelope
// ---------------------------------------------------------------------------

func TestEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"a": 1})
	var ok struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error any            `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("success body is not JSON: %v", err)
	}
	if !ok.OK || ok.Data["a"] != float64(1) || ok.Error != nil {
		t.Errorf("success envelope = %s, want {ok:true,data:{a:1}}", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	rec = httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "X_CODE", "x message")
	env := decodeEnvelope(t, rec)
	if env.OK || env.Error == nil || env.Error.Code != "X_CODE" || env.Error.Message != "x message" {
		t.Errorf("error envelope = %s", rec.Body.String())
	}
}

// Encoding straight into the ResponseWriter commits a 200 before the value is
// known to be serialisable, leaving a truncated body under a success status.
func TestWriteJSON_MarshalFailureIsNotATruncated200(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := decodeEnvelope(t, rec)
	if env.OK || env.Error == nil {
		t.Errorf("body = %s, want a complete error envelope", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// recoverer / statusWriter
// ---------------------------------------------------------------------------

func TestRecovererReturns500Envelope(t *testing.T) {
	h := recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if env := decodeEnvelope(t, rec); env.OK || env.Error == nil || env.Error.Code != codeInternal {
		t.Errorf("body = %s, want an INTERNAL_ERROR envelope", rec.Body.String())
	}
}

// ErrAbortHandler is how a handler says "drop this connection"; net/http must
// see it, so recoverer has to re-panic rather than convert it into a 500.
func TestRecovererRepanicsAbortHandler(t *testing.T) {
	h := recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		got := recover()
		if got != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler to propagate", got)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Error("ErrAbortHandler did not propagate")
}

// Writing an error envelope onto an already-committed response produces a
// "superfluous WriteHeader" log and appends garbage to a body the client has
// already begun parsing.
func TestRecovererDoesNotWriteTwice(t *testing.T) {
	h := recoverer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"data":{}}`))
		panic("boom after writing")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want the handler's 202 to stand", rec.Code)
	}
	if got := rec.Body.String(); got != `{"ok":true,"data":{}}` {
		t.Errorf("body = %q, want the handler's body untouched", got)
	}
}

// Wrapping a ResponseWriter hides its optional interfaces; a streaming
// endpoint added later would buffer forever.
func TestStatusWriterPassesThroughFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	if _, ok := any(sw).(http.Flusher); !ok {
		t.Fatal("statusWriter does not implement http.Flusher")
	}
	if _, ok := any(sw).(http.Hijacker); !ok {
		t.Fatal("statusWriter does not implement http.Hijacker")
	}
	_, _ = sw.Write([]byte("partial"))
	sw.Flush()
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying ResponseWriter")
	}
	// A recorder cannot hijack; the wrapper must report that rather than
	// panic on the type assertion.
	if _, _, err := sw.Hijack(); err == nil {
		t.Error("Hijack on a non-hijackable writer should return an error")
	}
}

func TestStatusWriterRecordsFirstStatusOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	sw.WriteHeader(http.StatusTeapot)
	sw.WriteHeader(http.StatusInternalServerError)
	if sw.status != http.StatusTeapot || rec.Code != http.StatusTeapot {
		t.Errorf("status = %d/%d, want the first WriteHeader to win", sw.status, rec.Code)
	}
}
