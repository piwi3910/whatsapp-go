// cmd/wa/proxy.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// proxyClient implements client.Service by forwarding to the REST API.
type proxyClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newProxyClient(baseURL, apiKey string) *proxyClient {
	return &proxyClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

func (p *proxyClient) do(method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, p.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return p.http.Do(req)
}

func (p *proxyClient) decodeResponse(resp *http.Response, target any) error {
	defer resp.Body.Close()
	var apiResp models.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if !apiResp.OK {
		if apiResp.Error != nil {
			return fmt.Errorf("%s: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return fmt.Errorf("request failed")
	}
	if target != nil {
		data, _ := json.Marshal(apiResp.Data)
		return json.Unmarshal(data, target)
	}
	return nil
}

// Implement Service interface methods by delegating to REST API.

func (p *proxyClient) Connect() error    { return nil } // server is already connected
func (p *proxyClient) Disconnect()       {}
func (p *proxyClient) IsConnected() bool { return true }

func (p *proxyClient) Status() client.ConnectionStatus {
	resp, err := p.do("GET", "/api/v1/auth/status", nil)
	if err != nil {
		return client.ConnectionStatus{State: "error"}
	}
	var status client.ConnectionStatus
	if err := p.decodeResponse(resp, &status); err != nil {
		return client.ConnectionStatus{State: "error"}
	}
	return status
}

func (p *proxyClient) Login() (<-chan client.QREvent, error) {
	return nil, fmt.Errorf("login must be done directly, not through proxy")
}

func (p *proxyClient) Logout() error {
	resp, err := p.do("POST", "/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}

func (p *proxyClient) SendText(jid, text string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "text", Content: text,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}

// sendMedia uploads media via the two-step flow (upload + send with media_id).
func (p *proxyClient) sendMedia(jid string, data []byte, filename, caption, msgType string) (*models.SendResponse, error) {
	// Step 1: Upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("writing media data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", p.baseURL+"/api/v1/media/upload", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	var uploadResult struct {
		MediaID string `json:"media_id"`
	}
	if err := p.decodeResponse(uploadResp, &uploadResult); err != nil {
		return nil, err
	}

	// Step 2: Send with media_id
	sendResp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: msgType, MediaID: uploadResult.MediaID, Caption: caption, Filename: filename,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(sendResp, &result)
}

func (p *proxyClient) SendImage(jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, caption, "image")
}
func (p *proxyClient) SendVideo(jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, caption, "video")
}
func (p *proxyClient) SendAudio(jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, "", "audio")
}
func (p *proxyClient) SendDocument(jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, "", "document")
}
func (p *proxyClient) SendSticker(jid string, data []byte) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, "sticker.webp", "", "sticker")
}
func (p *proxyClient) SendLocation(jid string, lat, lon float64, name string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "location", Lat: lat, Lon: lon, Name: name,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendContact(jid, contactJID string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "contact", ContactJID: contactJID,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendReaction(messageID, emoji string) error {
	resp, err := p.do("POST", fmt.Sprintf("/api/v1/messages/%s/react", messageID), map[string]string{"emoji": emoji})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DeleteMessage(messageID string, forEveryone bool) error {
	fe := ""
	if forEveryone {
		fe = "?for_everyone=true"
	}
	resp, err := p.do("DELETE", fmt.Sprintf("/api/v1/messages/%s%s", messageID, fe), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) MarkRead(messageID string) error {
	resp, err := p.do("POST", fmt.Sprintf("/api/v1/messages/%s/read", messageID), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	path := fmt.Sprintf("/api/v1/messages?jid=%s&limit=%d", chatJID, limit)
	if before > 0 {
		path += fmt.Sprintf("&before=%d", before)
	}
	resp, err := p.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Messages []models.Message `json:"messages"`
	}
	return result.Messages, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetMessage(messageID string) (*models.Message, error) {
	resp, err := p.do("GET", "/api/v1/messages/"+messageID, nil)
	if err != nil {
		return nil, err
	}
	var msg models.Message
	return &msg, p.decodeResponse(resp, &msg)
}
func (p *proxyClient) CreateGroup(name string, participants []string) (*models.Group, error) {
	resp, err := p.do("POST", "/api/v1/groups", map[string]any{"name": name, "participants": participants})
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) GetGroups() ([]models.Group, error) {
	resp, err := p.do("GET", "/api/v1/groups", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Groups []models.Group `json:"groups"`
	}
	return result.Groups, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetGroupInfo(groupJID string) (*models.Group, error) {
	resp, err := p.do("GET", "/api/v1/groups/"+groupJID, nil)
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) JoinGroup(inviteLink string) (string, error) {
	resp, err := p.do("POST", "/api/v1/groups/join", map[string]string{"invite_link": inviteLink})
	if err != nil {
		return "", err
	}
	var result struct {
		GroupJID string `json:"group_jid"`
	}
	return result.GroupJID, p.decodeResponse(resp, &result)
}
func (p *proxyClient) LeaveGroup(groupJID string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/leave", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetInviteLink(groupJID string) (string, error) {
	resp, err := p.do("GET", "/api/v1/groups/"+groupJID+"/invite-link", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		InviteLink string `json:"invite_link"`
	}
	return result.InviteLink, p.decodeResponse(resp, &result)
}
func (p *proxyClient) AddParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/add", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) RemoveParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/remove", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) PromoteParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/promote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DemoteParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/demote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetContacts() ([]models.Contact, error) {
	resp, err := p.do("GET", "/api/v1/contacts", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Contacts []models.Contact `json:"contacts"`
	}
	return result.Contacts, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetContactInfo(jid string) (*models.Contact, error) {
	resp, err := p.do("GET", "/api/v1/contacts/"+jid, nil)
	if err != nil {
		return nil, err
	}
	var contact models.Contact
	return &contact, p.decodeResponse(resp, &contact)
}
func (p *proxyClient) BlockContact(jid string) error {
	resp, err := p.do("POST", "/api/v1/contacts/"+jid+"/block", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) UnblockContact(jid string) error {
	resp, err := p.do("POST", "/api/v1/contacts/"+jid+"/unblock", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DownloadMedia(messageID string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", p.baseURL+"/api/v1/media/"+messageID, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}
func (p *proxyClient) RegisterEventHandler(handler func(models.Event)) {
	// Not supported via proxy — events come through polling
}
func (p *proxyClient) SetupEventHandlers() {
	// No-op for proxy — server handles event setup
}
