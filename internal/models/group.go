package models

type Group struct {
	JID          string        `json:"jid"`
	Name         string        `json:"name"`
	Topic        string        `json:"topic,omitempty"`
	Created      int64         `json:"created,omitempty"`
	Participants []Participant `json:"participants,omitempty"`
}

type Participant struct {
	JID          string `json:"jid"`
	IsAdmin      bool   `json:"is_admin"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}
