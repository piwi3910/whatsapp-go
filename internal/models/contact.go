package models

type Contact struct {
	JID       string `json:"jid"`
	Name      string `json:"name,omitempty"`
	PushName  string `json:"push_name,omitempty"`
	Status    string `json:"status,omitempty"`
	PictureID string `json:"picture_id,omitempty"`
}
