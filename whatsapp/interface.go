package whatsapp

import (
	"context"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// Service defines all WhatsApp operations used by the API and CLI layers.
//
// Every operation that performs I/O takes a context.Context as its first
// parameter so callers can cancel or deadline it. The methods that only
// inspect in-memory state (IsConnected, Status), tear down the connection
// (Disconnect), block on a timeout (WaitForConnection) or register
// callbacks (RegisterEventHandler, SetupEventHandlers) do not.
type Service interface {
	// Connection
	Connect(ctx context.Context) error
	Disconnect()
	IsConnected() bool
	WaitForConnection(timeout time.Duration) bool
	Status() ConnectionStatus

	// Auth
	Login(ctx context.Context) (<-chan QREvent, error)
	Logout(ctx context.Context) error

	// Messages
	SendText(ctx context.Context, jid, text string) (*models.SendResponse, error)
	SendImage(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendVideo(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendAudio(ctx context.Context, jid string, data []byte, filename string) (*models.SendResponse, error)
	SendDocument(ctx context.Context, jid string, data []byte, filename string) (*models.SendResponse, error)
	SendSticker(ctx context.Context, jid string, data []byte) (*models.SendResponse, error)
	SendLocation(ctx context.Context, jid string, lat, lon float64, name string) (*models.SendResponse, error)
	SendContact(ctx context.Context, jid, contactJID string) (*models.SendResponse, error)
	SendReaction(ctx context.Context, messageID, emoji string) error
	DeleteMessage(ctx context.Context, messageID string, forEveryone bool) error
	MarkRead(ctx context.Context, messageID string) error
	GetMessages(ctx context.Context, chatJID string, limit int, before int64) ([]models.Message, error)
	GetMessage(ctx context.Context, messageID string) (*models.Message, error)

	// Groups
	CreateGroup(ctx context.Context, name string, participants []string) (*models.Group, error)
	GetGroups(ctx context.Context) ([]models.Group, error)
	GetGroupInfo(ctx context.Context, groupJID string) (*models.Group, error)
	JoinGroup(ctx context.Context, inviteLink string) (string, error)
	LeaveGroup(ctx context.Context, groupJID string) error
	GetInviteLink(ctx context.Context, groupJID string) (string, error)
	AddParticipants(ctx context.Context, groupJID string, participants []string) error
	RemoveParticipants(ctx context.Context, groupJID string, participants []string) error
	PromoteParticipants(ctx context.Context, groupJID string, participants []string) error
	DemoteParticipants(ctx context.Context, groupJID string, participants []string) error

	// Contacts
	GetContacts(ctx context.Context) ([]models.Contact, error)
	GetContactInfo(ctx context.Context, jid string) (*models.Contact, error)
	BlockContact(ctx context.Context, jid string) error
	UnblockContact(ctx context.Context, jid string) error

	// Media
	DownloadMedia(ctx context.Context, messageID string) ([]byte, string, error) // data, mimeType, error

	// Events
	RegisterEventHandler(handler func(models.Event))
	SetupEventHandlers()
}

// ConnectionStatus represents the current connection state.
type ConnectionStatus struct {
	State       string `json:"state"` // "connecting", "connected", "disconnected", "logged_out"
	PhoneNumber string `json:"phone_number,omitempty"`
	PushName    string `json:"push_name,omitempty"`
}

// QREvent represents a QR code event during login.
type QREvent struct {
	Code string // QR code string for display
	Done bool   // true when login is complete
	Err  error  // non-nil when Done caused by a pairing error (issue #7)
}
