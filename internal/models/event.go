package models

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

type Event struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Payload   string `json:"payload"`
	Timestamp int64  `json:"timestamp"`
}
