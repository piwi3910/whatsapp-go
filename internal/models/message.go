package models

type Message struct {
	ID        string `json:"id"`
	ChatJID   string `json:"chat_jid"`
	SenderJID string `json:"sender_jid"`
	WaID      string `json:"wa_id"`
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	MediaSize int64  `json:"media_size,omitempty"`
	MediaURL  string `json:"-"`
	MediaKey  []byte `json:"-"`
	Caption   string `json:"caption,omitempty"`
	Timestamp int64  `json:"timestamp"`
	IsFromMe  bool   `json:"is_from_me"`
	IsRead    bool   `json:"is_read"`
	RawProto  []byte `json:"-"`
	CreatedAt int64  `json:"created_at"`
}

type SendRequest struct {
	To         string  `json:"to"`
	Type       string  `json:"type"`
	Content    string  `json:"content,omitempty"`
	MediaID    string  `json:"media_id,omitempty"`
	Caption    string  `json:"caption,omitempty"`
	Filename   string  `json:"filename,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
	Name       string  `json:"name,omitempty"`
	Emoji      string  `json:"emoji,omitempty"`
	ContactJID string  `json:"contact_jid,omitempty"`
}

type SendResponse struct {
	MessageID string `json:"message_id"`
	Timestamp int64  `json:"timestamp"`
}
