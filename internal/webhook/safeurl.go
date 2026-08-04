package webhook

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// Webhook URLs are supplied by API callers, and the server POSTs to them
// with no human in the loop. Without the checks in this file that makes the
// server a confused deputy: anyone who can register a webhook can aim the
// process at the cloud metadata endpoint (169.254.169.254), at a service
// only reachable inside the cluster, or back at the server's own admin API.
//
// Two layers, because either alone is bypassable:
//
//   - ValidateURL rejects obviously-bad targets at registration time, so
//     the caller gets a clear error instead of a silent never-delivering
//     webhook.
//   - safeDialer re-checks the resolved IP at the moment of the TCP dial,
//     which is what actually closes the hole: a hostname can pass the
//     registration check and resolve to a blocked address later (DNS
//     rebinding), and only the dial-time check sees the real address.
//
// Redirects are refused outright rather than re-validated, since following
// them re-opens the same hole one hop later.

// Policy decides which webhook targets are reachable.
//
// The split matters because "private" is not the same kind of danger as
// "link-local". Link-local, multicast and unspecified addresses are never a
// legitimate webhook target and are always refused. Loopback and private
// ranges, on the other hand, are exactly where a self-hosted receiver lives
// — an agent platform running in the same Kubernetes cluster, for instance
// — so they are refused by default (the safe choice for an
// internet-exposed server) but can be enabled deliberately.
type Policy struct {
	// AllowPrivateTargets permits webhook URLs that resolve to loopback or
	// private (RFC 1918 / unique-local) addresses. Enable it only when the
	// receiver is on a network you control, and remember that doing so lets
	// anyone who can register a webhook reach services on that network.
	AllowPrivateTargets bool
}

// ValidateURL reports whether a webhook target is acceptable under p. It
// rejects non-HTTP(S) schemes, missing hosts, and — when the host is a
// literal IP or resolves at registration time — any address the policy
// forbids.
//
// A hostname that does not resolve yet is allowed: the dial-time check
// still protects the actual request, and rejecting here would break
// legitimate targets whose DNS is not live at registration.
func (p Policy) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook URL has no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return p.checkIP(ip)
	}
	// Best-effort: if it resolves now, judge it now. Unresolvable is fine.
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range addrs {
		if err := p.checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// checkIP rejects addresses that this policy forbids as a webhook target.
func (p Policy) checkIP(ip net.IP) error {
	// Never allowed, whatever the policy: these reach infrastructure, not
	// applications.
	switch {
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// Covers 169.254.169.254, the cloud instance-metadata endpoint.
		return fmt.Errorf("webhook URL resolves to a link-local address (%s)", ip)
	case ip.IsMulticast():
		return fmt.Errorf("webhook URL resolves to a multicast address (%s)", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("webhook URL resolves to the unspecified address (%s)", ip)
	}
	if p.AllowPrivateTargets {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("webhook URL resolves to a loopback address (%s); "+
			"set webhooks.allow_private_targets to permit this", ip)
	case ip.IsPrivate():
		// Also covers AWS's IPv6 IMDS address fd00:ec2::254, which sits in
		// the fc00::/7 unique-local range.
		return fmt.Errorf("webhook URL resolves to a private address (%s); "+
			"set webhooks.allow_private_targets to permit this", ip)
	}
	return nil
}

// newSafeClient returns an HTTP client that refuses redirects and re-checks
// every address against p at dial time.
func newSafeClient(timeout time.Duration, p Policy) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs after DNS resolution, with the concrete address
		// about to be connected — the only place a rebinding attack is
		// visible. Returning an error here aborts the dial.
		Control: p.dialControl,
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return fmt.Errorf("webhook target redirected to %s; redirects are not followed", req.URL.Redacted())
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			DialContext:           dialer.DialContext,
		},
	}
}

// dialControl is installed on the dialer so every resolved address is
// checked immediately before connecting.
func (p Policy) dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook dial: unparsable address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webhook dial: unparsable IP %q", host)
	}
	return p.checkIP(ip)
}
