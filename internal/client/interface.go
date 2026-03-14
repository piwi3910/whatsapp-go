package client

import "github.com/piwi3910/whatsapp-go/internal/models"

// Service defines all WhatsApp operations used by the API and CLI layers.
type Service interface {
	// Connection
	Connect() error
	Disconnect()
	IsConnected() bool
	Status() ConnectionStatus

	// Auth
	Login() (<-chan QREvent, error)
	Logout() error

	// Messages
	SendText(jid, text string) (*models.SendResponse, error)
	SendImage(jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendVideo(jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendAudio(jid string, data []byte, filename string) (*models.SendResponse, error)
	SendDocument(jid string, data []byte, filename string) (*models.SendResponse, error)
	SendSticker(jid string, data []byte) (*models.SendResponse, error)
	SendLocation(jid string, lat, lon float64, name string) (*models.SendResponse, error)
	SendContact(jid, contactJID string) (*models.SendResponse, error)
	SendReaction(messageID, emoji string) error
	DeleteMessage(messageID string, forEveryone bool) error
	MarkRead(messageID string) error
	GetMessages(chatJID string, limit int, before int64) ([]models.Message, error)
	GetMessage(messageID string) (*models.Message, error)

	// Groups
	CreateGroup(name string, participants []string) (*models.Group, error)
	GetGroups() ([]models.Group, error)
	GetGroupInfo(groupJID string) (*models.Group, error)
	JoinGroup(inviteLink string) (string, error)
	LeaveGroup(groupJID string) error
	GetInviteLink(groupJID string) (string, error)
	AddParticipants(groupJID string, participants []string) error
	RemoveParticipants(groupJID string, participants []string) error
	PromoteParticipants(groupJID string, participants []string) error
	DemoteParticipants(groupJID string, participants []string) error

	// Contacts
	GetContacts() ([]models.Contact, error)
	GetContactInfo(jid string) (*models.Contact, error)
	BlockContact(jid string) error
	UnblockContact(jid string) error

	// Media
	DownloadMedia(messageID string) ([]byte, string, error) // data, mimeType, error

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
}
