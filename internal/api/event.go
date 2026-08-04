package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/store"
)

const (
	// defaultPageLimit is used when ?limit is absent.
	defaultPageLimit = 50
	// maxPageLimit caps ?limit on every paginated endpoint. A larger
	// value is clamped down to this rather than rejected, so a consumer
	// can safely ask for "as many as you'll give me". Without the cap a
	// single request could load the whole table into one JSON response.
	maxPageLimit = 500
)

// parseLimit reads the ?limit query parameter. A malformed or non-positive
// value is an error (400) rather than a silent fallback to the default,
// because a machine consumer that thinks it asked for 1000 and got 50 has
// no way to notice. Values above maxPageLimit are clamped.
func parseLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultPageLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > maxPageLimit {
		n = maxPageLimit
	}
	return n, nil
}

// handleListEvents serves the cursor-based event feed.
//
// Wire contract:
//
//	GET /api/v1/events?after=<cursor>&limit=<n>
//
//	200 {"ok":true,"data":{
//	      "events":[...],            // always an array, never null
//	      "cursor":"<next cursor>",  // echoes ?after when the page is empty
//	      "gap":false,
//	      "pruned_through":<int>     // highest event ID deleted by retention
//	    }}
//
//	400 INVALID_CURSOR / INVALID_LIMIT — malformed ?after or ?limit. Never
//	    silently coerced: a garbled cursor treated as 0 would replay the
//	    entire buffer.
//
//	410 EVENT_GAP — ?after points below the retention watermark, so events
//	    between it and the watermark were deleted before this consumer read
//	    them. Body:
//	      {"ok":false,
//	       "error":{"code":"EVENT_GAP","message":"..."},
//	       "data":{"gap":true,"cursor":"<safe cursor>","pruned_through":<int>}}
//	    The consumer has provably lost events. It must reconcile from
//	    GET /api/v1/messages (the message table is keyed by message
//	    identity and is not pruned) and then resume polling with
//	    after=<safe cursor>.
//
// Delivery is at-least-once. The store suppresses WhatsApp redeliveries of
// a message it has already logged, but a consumer that crashes between
// processing an event and persisting its cursor will see that event again.
// Use the "id" field of a message event's payload — the composite message
// ID — as the idempotency key.
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	if a := r.URL.Query().Get("after"); a != "" {
		n, err := strconv.ParseInt(a, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_CURSOR",
				"after must be a non-negative integer event ID")
			return
		}
		after = n
	}

	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", err.Error())
		return
	}

	events, err := s.store.GetEvents(after, limit)
	if err != nil {
		var gap *store.GapError
		if errors.As(err, &gap) {
			writeGapError(w, gap)
			return
		}
		writeServiceError(w, r, "event.list", err)
		return
	}

	prunedThrough, err := s.store.PrunedThroughEventID()
	if err != nil {
		writeServiceError(w, r, "event.pruned_through", err)
		return
	}

	// Echo the caller's cursor on an empty page. Returning "" here would
	// make a consumer that stores the cursor verbatim reset to 0 and
	// replay the whole buffer.
	cursor := strconv.FormatInt(after, 10)
	if len(events) > 0 {
		cursor = strconv.FormatInt(events[len(events)-1].ID, 10)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"events":         events,
		"cursor":         cursor,
		"gap":            false,
		"pruned_through": prunedThrough,
	})
}

// writeGapError emits the 410 Gone body documented on handleListEvents. It
// carries data alongside the error so the consumer gets the safe cursor to
// resume from without a second round trip.
func writeGapError(w http.ResponseWriter, gap *store.GapError) {
	// Via writeEnvelope so this shares the marshal-then-write ordering: a
	// half-written 410 would leave the consumer without the safe cursor it
	// needs to resume.
	writeEnvelope(w, http.StatusGone, models.APIResponse{
		OK: false,
		Error: &models.APIError{
			Code:    "EVENT_GAP",
			Message: gap.Error() + "; reconcile from /api/v1/messages, then resume from the cursor in data",
		},
		Data: map[string]any{
			"gap":            true,
			"cursor":         strconv.FormatInt(gap.PrunedThrough, 10),
			"pruned_through": gap.PrunedThrough,
		},
	})
}
