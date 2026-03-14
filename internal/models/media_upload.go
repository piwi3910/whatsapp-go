package models

type MediaUpload struct {
	ID        string `json:"id"`
	MimeType  string `json:"mime_type"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size"`
	Data      []byte `json:"-"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}
