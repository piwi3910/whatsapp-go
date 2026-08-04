// cmd/wa/proxy.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// proxyClient implements whatsapp.Service by forwarding to the REST API.
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

func (p *proxyClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
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

func (p *proxyClient) Connect(context.Context) error                { return nil }
func (p *proxyClient) Disconnect()                                  {}
func (p *proxyClient) IsConnected() bool                            { return true }
func (p *proxyClient) WaitForConnection(timeout time.Duration) bool { return true }

// Status has no ctx parameter in the Service interface (it is an in-memory
// read for the direct client), so the proxy's HTTP call gets a background
// context.
func (p *proxyClient) Status() whatsapp.ConnectionStatus {
	resp, err := p.do(context.Background(), "GET", "/api/v1/auth/status", nil)
	if err != nil {
		return whatsapp.ConnectionStatus{State: "error"}
	}
	var status whatsapp.ConnectionStatus
	if err := p.decodeResponse(resp, &status); err != nil {
		return whatsapp.ConnectionStatus{State: "error"}
	}
	return status
}

func (p *proxyClient) Login(context.Context) (<-chan whatsapp.QREvent, error) {
	return nil, fmt.Errorf("login must be done directly, not through proxy")
}

func (p *proxyClient) Logout(ctx context.Context) error {
	resp, err := p.do(ctx, "POST", "/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}

func (p *proxyClient) SendText(ctx context.Context, jid, text string) (*models.SendResponse, error) {
	resp, err := p.do(ctx, "POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "text", Content: text,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}

// sendMedia uploads media via the two-step flow (upload + send with media_id).
func (p *proxyClient) sendMedia(ctx context.Context, jid string, data []byte, filename, caption, msgType string) (*models.SendResponse, error) {
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

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/v1/media/upload", body)
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
	sendResp, err := p.do(ctx, "POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: msgType, MediaID: uploadResult.MediaID, Caption: caption, Filename: filename,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(sendResp, &result)
}

func (p *proxyClient) SendImage(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(ctx, jid, data, filename, caption, "image")
}
func (p *proxyClient) SendVideo(ctx context.Context, jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(ctx, jid, data, filename, caption, "video")
}
func (p *proxyClient) SendAudio(ctx context.Context, jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(ctx, jid, data, filename, "", "audio")
}
func (p *proxyClient) SendDocument(ctx context.Context, jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(ctx, jid, data, filename, "", "document")
}
func (p *proxyClient) SendSticker(ctx context.Context, jid string, data []byte) (*models.SendResponse, error) {
	return p.sendMedia(ctx, jid, data, "sticker.webp", "", "sticker")
}
func (p *proxyClient) SendLocation(ctx context.Context, jid string, lat, lon float64, name string) (*models.SendResponse, error) {
	resp, err := p.do(ctx, "POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "location", Lat: lat, Lon: lon, Name: name,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendContact(ctx context.Context, jid, contactJID string) (*models.SendResponse, error) {
	resp, err := p.do(ctx, "POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "contact", ContactJID: contactJID,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendReaction(ctx context.Context, messageID, emoji string) error {
	resp, err := p.do(ctx, "POST", fmt.Sprintf("/api/v1/messages/%s/react", messageID), map[string]string{"emoji": emoji})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DeleteMessage(ctx context.Context, messageID string, forEveryone bool) error {
	fe := ""
	if forEveryone {
		fe = "?for_everyone=true"
	}
	resp, err := p.do(ctx, "DELETE", fmt.Sprintf("/api/v1/messages/%s%s", messageID, fe), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) MarkRead(ctx context.Context, messageID string) error {
	resp, err := p.do(ctx, "POST", fmt.Sprintf("/api/v1/messages/%s/read", messageID), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetMessages(ctx context.Context, chatJID string, limit int, before int64) ([]models.Message, error) {
	path := fmt.Sprintf("/api/v1/messages?jid=%s&limit=%d", chatJID, limit)
	if before > 0 {
		path += fmt.Sprintf("&before=%d", before)
	}
	resp, err := p.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Messages []models.Message `json:"messages"`
	}
	return result.Messages, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetMessage(ctx context.Context, messageID string) (*models.Message, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/messages/"+messageID, nil)
	if err != nil {
		return nil, err
	}
	var msg models.Message
	return &msg, p.decodeResponse(resp, &msg)
}
func (p *proxyClient) CreateGroup(ctx context.Context, name string, participants []string) (*models.Group, error) {
	resp, err := p.do(ctx, "POST", "/api/v1/groups", map[string]any{"name": name, "participants": participants})
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) GetGroups(ctx context.Context) ([]models.Group, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/groups", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Groups []models.Group `json:"groups"`
	}
	return result.Groups, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetGroupInfo(ctx context.Context, groupJID string) (*models.Group, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/groups/"+groupJID, nil)
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) JoinGroup(ctx context.Context, inviteLink string) (string, error) {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/join", map[string]string{"invite_link": inviteLink})
	if err != nil {
		return "", err
	}
	var result struct {
		GroupJID string `json:"group_jid"`
	}
	return result.GroupJID, p.decodeResponse(resp, &result)
}
func (p *proxyClient) LeaveGroup(ctx context.Context, groupJID string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/"+groupJID+"/leave", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetInviteLink(ctx context.Context, groupJID string) (string, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/groups/"+groupJID+"/invite-link", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		InviteLink string `json:"invite_link"`
	}
	return result.InviteLink, p.decodeResponse(resp, &result)
}
func (p *proxyClient) AddParticipants(ctx context.Context, groupJID string, participants []string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/"+groupJID+"/participants/add", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) RemoveParticipants(ctx context.Context, groupJID string, participants []string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/"+groupJID+"/participants/remove", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) PromoteParticipants(ctx context.Context, groupJID string, participants []string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/"+groupJID+"/participants/promote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DemoteParticipants(ctx context.Context, groupJID string, participants []string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/groups/"+groupJID+"/participants/demote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetContacts(ctx context.Context) ([]models.Contact, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/contacts", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Contacts []models.Contact `json:"contacts"`
	}
	return result.Contacts, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetContactInfo(ctx context.Context, jid string) (*models.Contact, error) {
	resp, err := p.do(ctx, "GET", "/api/v1/contacts/"+jid, nil)
	if err != nil {
		return nil, err
	}
	var contact models.Contact
	return &contact, p.decodeResponse(resp, &contact)
}
func (p *proxyClient) BlockContact(ctx context.Context, jid string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/contacts/"+jid+"/block", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) UnblockContact(ctx context.Context, jid string) error {
	resp, err := p.do(ctx, "POST", "/api/v1/contacts/"+jid+"/unblock", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DownloadMedia(ctx context.Context, messageID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/v1/media/"+messageID, nil)
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
