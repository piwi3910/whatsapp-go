package models

import "encoding/json"

const (
	EventMessageReceived          = "message.received"
	EventMessageSent              = "message.sent"
	EventMessageDeleted           = "message.deleted"
	EventMessageReaction          = "message.reaction"
	EventMessageRead              = "message.read"
	EventGroupCreated             = "group.created"
	EventGroupUpdated             = "group.updated"
	EventGroupParticipantAdded    = "group.participant_added"
	EventGroupParticipantRemoved  = "group.participant_removed"
	EventGroupParticipantPromoted = "group.participant_promoted"
	EventGroupParticipantDemoted  = "group.participant_demoted"
	EventContactUpdated           = "contact.updated"
	EventPresenceUpdated          = "presence.updated"
	EventConnectionLoggedOut      = "connection.logged_out"
	EventConnectionConnected      = "connection.connected"
	EventConnectionDisconnected   = "connection.disconnected"
)

// Event is one entry in the durable event log.
//
// Payload is a JSON document — for message events a marshalled Message, so
// its "id" field is the composite message ID consumers should use as their
// idempotency key. It is json.RawMessage rather than a string so it
// serialises as a nested object: a consumer reads event.payload.id directly
// instead of decoding a string and then decoding that. This also makes the
// event-poll API agree with webhook deliveries, which already embedded the
// payload as nested JSON.
type Event struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`

	// DedupeKey is the natural idempotency key for this event, or "" for
	// events that have none. Producers may leave it empty: the store
	// derives it for message events from the payload's message ID and uses
	// it to suppress whatsmeow redelivering the same message after a
	// reconnect or history sync.
	DedupeKey string `json:"dedupe_key,omitempty"`
}
