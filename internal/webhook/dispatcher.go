package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// Dispatcher manages webhook delivery with retries and HMAC signing.
type Dispatcher struct {
	mu       sync.RWMutex
	webhooks map[string]models.Webhook
	client   *http.Client
	policy   Policy
}

// New creates a Dispatcher that refuses loopback and private targets — the
// safe default for a server that may be internet-reachable. Use
// NewWithPolicy when the receiver is deliberately on a private network.
func New() *Dispatcher {
	return NewWithPolicy(Policy{})
}

// NewWithPolicy creates a Dispatcher whose outbound requests are governed by
// p, both at registration (Policy.ValidateURL) and at dial time.
func NewWithPolicy(p Policy) *Dispatcher {
	return &Dispatcher{
		webhooks: make(map[string]models.Webhook),
		policy:   p,
		client:   newSafeClient(10*time.Second, p),
	}
}

// Policy returns the dispatcher's target policy, so callers validating a URL
// before registering it apply exactly the rules the dispatcher enforces.
func (d *Dispatcher) Policy() Policy { return d.policy }

// Register adds a webhook.
func (d *Dispatcher) Register(wh models.Webhook) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.webhooks[wh.ID] = wh
}

// Unregister removes a webhook.
func (d *Dispatcher) Unregister(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.webhooks, id)
}

// Dispatch sends an event to all matching webhooks asynchronously.
func (d *Dispatcher) Dispatch(evt models.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, wh := range d.webhooks {
		if !matchesEvent(wh.Events, evt.Type) {
			continue
		}
		// Deliver asynchronously
		go d.deliver(wh, evt)
	}
}

func matchesEvent(subscribed []string, eventType string) bool {
	for _, s := range subscribed {
		if s == "*" || s == eventType {
			return true
		}
	}
	return false
}

// webhookPayload is the JSON body sent to webhook URLs.
type webhookPayload struct {
	Event     string          `json:"event"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

func (d *Dispatcher) deliver(wh models.Webhook, evt models.Event) {
	payload := webhookPayload{
		Event:     evt.Type,
		Timestamp: evt.Timestamp,
		Data:      json.RawMessage(evt.Payload),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook %s: marshal error: %v", wh.ID, err)
		return
	}

	// Retry schedule: immediate, 1s, 5s, 15s
	delays := []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}

	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}

		req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("webhook %s: request error: %v", wh.ID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		// Add HMAC signature if secret is configured
		if wh.Secret != "" {
			mac := hmac.New(sha256.New, []byte(wh.Secret))
			mac.Write(body)
			sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Wa-Signature", sig)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			log.Printf("webhook %s: attempt %d failed: %v", wh.ID, attempt+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // Success
		}
		log.Printf("webhook %s: attempt %d status %d", wh.ID, attempt+1, resp.StatusCode)
	}

	log.Printf("webhook %s: all delivery attempts failed for event %s", wh.ID, evt.Type)
}
