package research

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Page reading is the one place where LifeOS follows an address it did not
// choose: a search result, a link the model proposed, a redirect on a page.
// That address can point at the machine LifeOS runs on — a local admin panel, a
// database port, a cloud metadata endpoint — so every hop is checked against
// the public internet before a connection is made.

// errPrivateAddress is returned when a page address leads somewhere local.
var errPrivateAddress = errors.New("адрес ведёт во внутреннюю сеть")

// maxPageRedirects bounds how many times a page may redirect.
const maxPageRedirects = 5

// publicHostGuard decides whether a host may be contacted for page reading.
// allowHost is the one host the user configured themselves (a search endpoint),
// which may legitimately live on the local network; everything else must be a
// public address.
type publicHostGuard struct {
	allowHost string
	resolver  *net.Resolver
}

// checkURL validates the address before any connection is made.
func (g publicHostGuard) checkURL(ctx context.Context, target *url.URL) error {
	if target == nil {
		return errPrivateAddress
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("адрес страницы некорректен: %q", target.String())
	}
	host := strings.ToLower(target.Hostname())
	if host == "" {
		return fmt.Errorf("адрес страницы некорректен: %q", target.String())
	}
	if g.allowHost != "" && strings.EqualFold(host, g.allowHost) {
		return nil
	}
	_, err := g.resolvePublic(ctx, host)
	return err
}

// dial checks the address again at connection time and connects to the address
// that was checked. Resolving twice would leave a window for DNS rebinding: the
// name would pass the check, then resolve somewhere local for the real dial.
func (g publicHostGuard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if g.allowHost != "" && strings.EqualFold(host, g.allowHost) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, address)
	}
	ips, err := g.resolvePublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errPrivateAddress
	}
	return nil, lastErr
}

// resolvePublic resolves a host and returns its addresses, refusing the whole
// host if any of them is not public: one local answer is enough to make the
// name untrustworthy.
func (g publicHostGuard) resolvePublic(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicAddress(ip) {
			return nil, fmt.Errorf("%w: %s", errPrivateAddress, host)
		}
		return []net.IP{ip}, nil
	}
	resolver := g.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("адрес %s не разрешён: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("адрес %s не разрешён", host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if !isPublicAddress(addr.IP) {
			return nil, fmt.Errorf("%w: %s", errPrivateAddress, host)
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

// isPublicAddress reports whether an address belongs to the public internet.
// Loopback, private, link-local (which includes the cloud metadata endpoint),
// carrier-grade NAT, multicast and unspecified addresses are not public.
func isPublicAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 is carrier-grade NAT, which Go does not treat as
		// private but which is never a public website.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// 192.0.0.0/24 is IETF protocol assignment, not a website.
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return false
		}
	}
	return true
}

// checkSourceEndpoint refuses a connected source that points at the link-local
// range, where cloud metadata endpoints live. A source is an address the user
// typed, so loopback and private addresses stay allowed — a local service is a
// legitimate source — but nothing legitimate lives at 169.254.0.0/16, and
// reaching it is the classic escalation from an allowed address.
func (g publicHostGuard) checkSourceEndpoint(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return nil
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isMetadataAddress(ip) {
			return fmt.Errorf("%w: %s — адрес служебного сервиса", errPrivateAddress, host)
		}
		return nil
	}
	resolver := g.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		// A name that does not resolve fails at the call itself, with a better
		// message than this pre-check could give.
		return nil
	}
	for _, addr := range addrs {
		if isMetadataAddress(addr.IP) {
			return fmt.Errorf("%w: %s — адрес служебного сервиса", errPrivateAddress, host)
		}
	}
	return nil
}

// metadataAddress is the IPv6 metadata endpoint some clouds publish. It is a
// unique-local address, so it is not link-local by classification, but it is
// never a service a source should reach.
var metadataAddress = net.ParseIP("fd00:ec2::254")

// isMetadataAddress reports whether an address belongs to a service-metadata
// range rather than to a real service.
func isMetadataAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.Equal(metadataAddress) {
		return true
	}
	return false
}

// guardedClient builds the HTTP client used for page reading: every request and
// every redirect goes through the same check.
func (c *Client) guardedClient(allowHost string) *http.Client {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	guard := publicHostGuard{allowHost: strings.ToLower(allowHost)}
	if c.allowPrivateHosts {
		// The guard is off, so the configured client is used as it is: an
		// injected transport keeps working, which is what tests rely on.
		return c.http
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           guard.dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxPageRedirects {
				return fmt.Errorf("страница перенаправляет слишком много раз")
			}
			return guard.checkURL(request.Context(), request.URL)
		},
	}
}
