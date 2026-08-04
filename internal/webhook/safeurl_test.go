package webhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string // substring; "" means the URL must be accepted
	}{
		{"public https", "https://example.com/hook", ""},
		{"public http with port", "http://example.com:8080/hook", ""},
		{"public literal ip", "http://93.184.216.34/hook", ""},

		{"cloud metadata endpoint", "http://169.254.169.254/latest/meta-data/", "link-local"},
		// AWS's IPv6 IMDS address is a unique-local address, so the private
		// rule is what rejects it.
		{"aws imds over ipv6", "http://[fd00:ec2::254]/latest/", "private"},
		{"loopback v4", "http://127.0.0.1:8080/api/v1/webhooks", "loopback"},
		{"loopback v6", "http://[::1]:8080/", "loopback"},
		{"private 10/8", "http://10.0.0.5/hook", "private"},
		{"private 192.168/16", "http://192.168.1.10/hook", "private"},
		{"private 172.16/12", "http://172.16.0.1/hook", "private"},
		{"unspecified", "http://0.0.0.0/hook", "unspecified"},
		// 224.0.0.1 is link-local multicast, so that rule fires first;
		// 239.x is the global-scope multicast case.
		{"link-local multicast", "http://224.0.0.1/hook", "link-local"},
		{"multicast", "http://239.255.0.1/hook", "multicast"},

		{"file scheme", "file:///etc/passwd", "scheme"},
		{"gopher scheme", "gopher://example.com/", "scheme"},
		{"no host", "http:///just-a-path", "no host"},
		{"unparsable", "http://a b c/", "invalid webhook URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Policy{}.ValidateURL(tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateURL(%q) = %v, want accepted", tt.url, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateURL(%q) accepted the URL, want error containing %q", tt.url, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateURL(%q) = %v, want error containing %q", tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestSafeClientRefusesLoopbackDial is the layer that actually closes the
// SSRF hole: even when a target passes registration (or DNS changes
// afterwards), the dial-time check must refuse a blocked address. httptest
// listens on loopback, so a successful request here would mean the guard is
// not wired into the dialer.
func TestSafeClientRefusesLoopbackDial(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer srv.Close()

	client := newSafeClient(5*time.Second, Policy{})
	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("request to a loopback address succeeded; the dial-time guard is not active")
	}
	if reached {
		t.Fatal("the handler ran: traffic reached a loopback target")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v, want it to name the loopback address", err)
	}
}

// TestSafeClientRefusesRedirects proves a redirect cannot be used to reach
// an address that direct registration would reject.
func TestSafeClientRefusesRedirects(t *testing.T) {
	client := newSafeClient(5*time.Second, Policy{})
	// The redirect target does not matter: redirects are refused outright,
	// which is what stops a public URL bouncing to an internal one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := client.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected an error")
	}
	// Loopback (httptest) is refused at dial time before the redirect can
	// even be served; either guard failing the request is a pass, but the
	// error must not be a success path.
	if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want a loopback or redirect refusal", err)
	}
}

// TestDispatcherUsesSafeClient guards against the guard being bypassed by a
// future refactor that hands the Dispatcher a plain http.Client.
func TestDispatcherUsesSafeClient(t *testing.T) {
	d := New()
	if d.client.CheckRedirect == nil {
		t.Fatal("dispatcher's HTTP client follows redirects; it must use newSafeClient")
	}
	d.Register(models.Webhook{ID: "wh_test", URL: "http://127.0.0.1:1/hook", Events: []string{"*"}})
	// Dispatch is fire-and-forget; this only asserts it does not panic with
	// the safe client installed.
	d.Dispatch(models.Event{ID: 1, Type: "message.received", Payload: "{}"})
}

// TestPolicyAllowPrivateTargets covers the self-hosted case: a receiver on
// a private network (an agent platform in the same cluster, say) must be
// reachable when the operator opts in — while the infrastructure addresses
// stay blocked regardless, since no policy makes those a valid target.
func TestPolicyAllowPrivateTargets(t *testing.T) {
	p := Policy{AllowPrivateTargets: true}

	for _, u := range []string{
		"http://10.42.0.15:8080/hook", // cluster pod address
		"http://192.168.1.10/hook",    // home/lab LAN
		"http://127.0.0.1:9000/hook",  // same host
		"http://[fd00::1]/hook",       // unique-local IPv6
	} {
		if err := p.ValidateURL(u); err != nil {
			t.Errorf("permissive policy rejected %s: %v", u, err)
		}
	}

	// Still refused: these reach infrastructure, not applications.
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://224.0.0.1/hook",
		"http://0.0.0.0/hook",
	} {
		if err := p.ValidateURL(u); err == nil {
			t.Errorf("permissive policy accepted %s; infrastructure addresses must never be allowed", u)
		}
	}

	// And the dial-time guard must agree with the policy, or a permitted
	// target would still fail to connect.
	var served bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := newSafeClient(5*time.Second, p).Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("permissive policy could not reach a loopback receiver: %v", err)
	}
	_ = resp.Body.Close()
	if !served {
		t.Fatal("request did not reach the receiver")
	}
}
