package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/store"
)

func pagingServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Server{store: st}
}

// pagingEnvelope unwraps the full {"ok","data","error"} response envelope.
type pagingEnvelope struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) pagingEnvelope {
	t.Helper()
	var env pagingEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response body is not JSON: %v (%s)", err, rec.Body.String())
	}
	return env
}

func getEvents(t *testing.T, s *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleListEvents(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events?"+query, nil))
	return rec
}

func getMessages(t *testing.T, s *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleListMessages(rec, httptest.NewRequest(http.MethodGet, "/api/v1/messages?"+query, nil))
	return rec
}

func seedEvents(t *testing.T, s *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		evt := &models.Event{
			Type:      models.EventMessageReceived,
			Payload:   json.RawMessage(fmt.Sprintf(`{"id":"msg-%d"}`, i)),
			Timestamp: int64(i),
		}
		if err := s.store.InsertEvent(evt); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}
}

func seedMessages(t *testing.T, s *Server, chat string, n int, perSecond int) {
	t.Helper()
	for i := 0; i < n; i++ {
		msg := &models.Message{
			ID:        fmt.Sprintf("msg-%03d", i),
			ChatJID:   chat,
			SenderJID: "sender@s.whatsapp.net",
			WaID:      fmt.Sprintf("wa-%03d", i),
			Type:      "text",
			Timestamp: int64(1000 + i/perSecond),
		}
		if err := s.store.InsertMessage(msg); err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}
}

// A garbled cursor used to be discarded and treated as after=0, which
// silently replays the entire buffer to a machine consumer.
func TestListEvents_MalformedCursorIsRejected(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, 3)

	for _, q := range []string{"after=abc", "after=12x", "after=-1", "after=1.5", "after=99999999999999999999"} {
		rec := getEvents(t, s, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", q, rec.Code, rec.Body.String())
			continue
		}
		env := decodeEnvelope(t, rec)
		if env.OK || env.Error == nil || env.Error.Code != "INVALID_CURSOR" {
			t.Errorf("%s: body = %s, want an INVALID_CURSOR error", q, rec.Body.String())
		}
	}
}

func TestListEvents_MalformedLimitIsRejected(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, 3)

	for _, q := range []string{"limit=abc", "limit=0", "limit=-5", "limit="} {
		rec := getEvents(t, s, q)
		if q == "limit=" {
			// An empty value means "unset" and falls back to the default.
			if rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200", q, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
			continue
		}
		if env := decodeEnvelope(t, rec); env.Error == nil || env.Error.Code != "INVALID_LIMIT" {
			t.Errorf("%s: body = %s, want an INVALID_LIMIT error", q, rec.Body.String())
		}
	}
}

// An unbounded limit lets one request load the whole table into a single
// JSON response.
func TestListEvents_LimitIsClamped(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, maxPageLimit+5)

	rec := getEvents(t, s, "limit=10000000")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	events, ok := env.Data["events"].([]any)
	if !ok {
		t.Fatalf("data.events is not an array: %s", rec.Body.String())
	}
	if len(events) != maxPageLimit {
		t.Errorf("returned %d events, want the clamp of %d", len(events), maxPageLimit)
	}
}

// A consumer that stores whatever cursor it gets back must not be reset to
// the start of the log by an empty poll.
func TestListEvents_EmptyPageEchoesCursor(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, 3)

	rec := getEvents(t, s, "after=3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	env := decodeEnvelope(t, rec)
	if got := env.Data["cursor"]; got != "3" {
		t.Errorf("cursor = %v, want the input cursor %q echoed back", got, "3")
	}
	events, ok := env.Data["events"].([]any)
	if !ok || len(events) != 0 {
		t.Errorf("data.events = %v, want an empty array (never null)", env.Data["events"])
	}
	if env.Data["gap"] != false {
		t.Errorf("gap = %v, want false", env.Data["gap"])
	}
}

func TestListEvents_EmptyLogReturnsArrayNotNull(t *testing.T) {
	s := pagingServer(t)
	rec := getEvents(t, s, "")
	if got := rec.Body.String(); !jsonHasEmptyArray(got, "events") {
		t.Errorf("body = %s, want data.events to be []", got)
	}
}

func jsonHasEmptyArray(body, field string) bool {
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return false
	}
	return string(env.Data[field]) == "[]"
}

// The blocker: retention deleting events a consumer had not read yet used
// to be invisible — it simply got rows from past the hole.
func TestListEvents_GapReturns410WithSafeCursor(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, 20)
	if err := s.store.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}

	rec := getEvents(t, s, "after=3")
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 Gone (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env.OK {
		t.Error("gap response should not be ok:true")
	}
	if env.Error == nil || env.Error.Code != "EVENT_GAP" {
		t.Fatalf("error = %+v, want code EVENT_GAP", env.Error)
	}
	if env.Data["gap"] != true {
		t.Errorf("data.gap = %v, want true", env.Data["gap"])
	}
	if got := env.Data["cursor"]; got != "10" {
		t.Errorf("data.cursor = %v, want the safe cursor %q", got, "10")
	}
	if got := env.Data["pruned_through"]; got != float64(10) {
		t.Errorf("data.pruned_through = %v, want 10", got)
	}

	// Resuming from the advertised safe cursor works and reports no gap.
	rec = getEvents(t, s, "after=10")
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env = decodeEnvelope(t, rec)
	if events, _ := env.Data["events"].([]any); len(events) != 10 {
		t.Errorf("resume returned %d events, want 10", len(events))
	}
	if env.Data["gap"] != false {
		t.Errorf("resume gap = %v, want false", env.Data["gap"])
	}
}

// A consumer starting from scratch is not claiming to have processed
// anything, so it gets the retained tail rather than a 410.
func TestListEvents_FreshConsumerGetsNoGap(t *testing.T) {
	s := pagingServer(t)
	seedEvents(t, s, 20)
	if err := s.store.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}

	rec := getEvents(t, s, "after=0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if got := env.Data["pruned_through"]; got != float64(10) {
		t.Errorf("pruned_through = %v, want 10", got)
	}
}

func TestListMessages_MalformedParamsAreRejected(t *testing.T) {
	s := pagingServer(t)
	seedMessages(t, s, "12345678901@s.whatsapp.net", 3, 1)

	tests := []struct {
		query string
		code  string
	}{
		{"jid=12345678901@s.whatsapp.net&before=abc", "INVALID_CURSOR"},
		{"jid=12345678901@s.whatsapp.net&before=-1", "INVALID_CURSOR"},
		{"jid=12345678901@s.whatsapp.net&before=1000.", "INVALID_CURSOR"},
		{"jid=12345678901@s.whatsapp.net&before=x.msg-1", "INVALID_CURSOR"},
		{"jid=12345678901@s.whatsapp.net&limit=abc", "INVALID_LIMIT"},
		{"jid=12345678901@s.whatsapp.net&limit=0", "INVALID_LIMIT"},
	}
	for _, tc := range tests {
		rec := getMessages(t, s, tc.query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", tc.query, rec.Code, rec.Body.String())
			continue
		}
		if env := decodeEnvelope(t, rec); env.Error == nil || env.Error.Code != tc.code {
			t.Errorf("%s: body = %s, want %s", tc.query, rec.Body.String(), tc.code)
		}
	}
}

func TestListMessages_LimitIsClamped(t *testing.T) {
	s := pagingServer(t)
	seedMessages(t, s, "12345678901@s.whatsapp.net", maxPageLimit+5, 10)

	rec := getMessages(t, s, "jid=12345678901@s.whatsapp.net&limit=999999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	msgs, ok := env.Data["messages"].([]any)
	if !ok {
		t.Fatalf("data.messages is not an array: %s", rec.Body.String())
	}
	if len(msgs) != maxPageLimit {
		t.Errorf("returned %d messages, want the clamp of %d", len(msgs), maxPageLimit)
	}
}

// Walking history through the returned cursor must visit every message
// exactly once, even though several messages share each one-second
// timestamp.
func TestListMessages_CursorWalkLosesNothing(t *testing.T) {
	s := pagingServer(t)
	const chat = "12345678901@s.whatsapp.net"
	const total = 20
	seedMessages(t, s, chat, total, 5)

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 100; page++ {
		rec := getMessages(t, s, "jid="+chat+"&limit=3&before="+cursor)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status = %d (%s)", page, rec.Code, rec.Body.String())
		}
		env := decodeEnvelope(t, rec)
		msgs, _ := env.Data["messages"].([]any)
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			seen[m.(map[string]any)["id"].(string)]++
		}
		next, _ := env.Data["cursor"].(string)
		if next == cursor {
			t.Fatalf("page %d: cursor did not advance (%q)", page, cursor)
		}
		cursor = next
	}

	if len(seen) != total {
		t.Errorf("walked %d distinct messages, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("message %s returned %d times", id, n)
		}
	}
}

func TestListMessages_EmptyPageEchoesCursor(t *testing.T) {
	s := pagingServer(t)
	seedMessages(t, s, "12345678901@s.whatsapp.net", 2, 1)

	rec := getMessages(t, s, "jid=12345678901@s.whatsapp.net&before=1.msg-000")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if got := env.Data["cursor"]; got != "1.msg-000" {
		t.Errorf("cursor = %v, want the input cursor echoed back", got)
	}
	if msgs, ok := env.Data["messages"].([]any); !ok || len(msgs) != 0 {
		t.Errorf("data.messages = %v, want an empty array (never null)", env.Data["messages"])
	}
}

func TestListMessages_LegacyTimestampCursorStillAccepted(t *testing.T) {
	s := pagingServer(t)
	seedMessages(t, s, "12345678901@s.whatsapp.net", 10, 1)

	rec := getMessages(t, s, "jid=12345678901@s.whatsapp.net&before=1005")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	msgs, _ := env.Data["messages"].([]any)
	if len(msgs) != 5 {
		t.Errorf("got %d messages older than ts 1005, want 5", len(msgs))
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", defaultPageLimit, false},
		{"10", 10, false},
		{fmt.Sprint(maxPageLimit), maxPageLimit, false},
		{fmt.Sprint(maxPageLimit + 1), maxPageLimit, false},
		{"10000000", maxPageLimit, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodGet, "/?limit="+tc.raw, nil)
		got, err := parseLimit(r)
		if (err != nil) != tc.wantErr {
			t.Errorf("limit=%q: err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("limit=%q: got %d want %d", tc.raw, got, tc.want)
		}
	}
}
