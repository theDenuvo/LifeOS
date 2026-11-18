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

// Everything LifeOS sends out follows an address it did not choose: a search
// result, a link the model proposed, a redirect on a page or on a call to a
// connected source. That address can point at the machine LifeOS runs on — a
// local admin panel, a database port, a cloud metadata endpoint — so one guard
// decides every hop of every request: page reading, web search and the calls
// to connected sources all go through it, and the connection is made to the
// address the guard checked.

// errPrivateAddress is returned when an address leads somewhere local.
var errPrivateAddress = errors.New("адрес ведёт во внутреннюю сеть")

// maxPageRedirects bounds how many times a request may redirect.
const maxPageRedirects = 5

// netPolicy says where traffic of one kind is allowed to go.
type netPolicy int

const (
	// policyPublicPage is page reading: the address came from a search result
	// or from the model, so it must lead to the public internet and nowhere
	// else.
	policyPublicPage netPolicy = iota
	// policyUserSource is an endpoint the user typed: a search endpoint, an
	// HTTP API, an MCP server. It may live on the user's own network — a
	// self-hosted search service is a normal thing to connect — but only that
	// one host is local: a redirect to another machine of the internal network
	// is not the source the user named, and a service address is never allowed.
	policyUserSource
)

// Resolver turns a host name into addresses. Tests replace it to describe a
// network that does not exist, including one whose DNS answers change between
// two lookups.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// publicHostGuard decides which addresses LifeOS may contact and dials only
// addresses it has checked. Resolving a name twice — once to check, once to
// connect — would leave a window for DNS rebinding, where the second answer
// points somewhere local.
type publicHostGuard struct {
	policy     netPolicy
	originHost string
	// allowLocal accepts non-public addresses everywhere. It exists for tests
	// and for a single-user build that reads its own network on purpose; a
	// service address stays refused even then.
	allowLocal bool
	resolver   Resolver
}

// checkURL validates an address before any connection is made to it.
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
	_, err := g.resolveChecked(ctx, host)
	return err
}

// dial checks the address again at connection time and connects to the address
// that was checked.
func (g publicHostGuard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := g.resolveChecked(ctx, host)
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

// resolveChecked resolves a host and returns its addresses, refusing the host
// when any of them is off limits: one bad answer is enough to make the name
// untrustworthy.
func (g publicHostGuard) resolveChecked(ctx context.Context, host string) ([]net.IP, error) {
	local := g.allowLocal || (g.policy == policyUserSource && strings.EqualFold(host, g.originHost))
	if ip := net.ParseIP(host); ip != nil {
		if err := g.checkIP(ip, host, local); err != nil {
			return nil, err
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
		if err := g.checkIP(addr.IP, host, local); err != nil {
			return nil, err
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

// checkIP applies the rules to one resolved address.
func (g publicHostGuard) checkIP(ip net.IP, host string, local bool) error {
	if isMetadataAddress(ip) {
		// A service address is refused in every policy: it answers about the
		// machine LifeOS runs on, and nothing legitimate is hosted there.
		return fmt.Errorf("%w: %s — адрес служебного сервиса", errPrivateAddress, host)
	}
	if !local && !isPublicAddress(ip) {
		return fmt.Errorf("%w: %s", errPrivateAddress, host)
	}
	return nil
}

// isPublicAddress reports whether an address belongs to the public internet.
// Loopback, private, link-local, carrier-grade NAT, multicast and unspecified
// addresses are not public.
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

// metadataAddresses are the fixed service addresses clouds answer on. The
// link-local range covers most of them (169.254.169.254 and friends); the
// others sit in ranges a private-source policy would otherwise allow.
var metadataAddresses = map[string]bool{
	"100.100.100.200": true, // Alibaba Cloud ECS
	"192.0.0.192":     true, // Oracle Cloud
	"fd00:ec2::254":   true, // Amazon EC2 (IPv6)
}

// isMetadataAddress reports whether an address belongs to a service-metadata
// endpoint rather than to a real service.
func isMetadataAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	return metadataAddresses[strings.ToLower(ip.String())]
}

// guardedClient builds the HTTP client for one kind of traffic: every request
// and every redirect goes through the same check, and the connection is made
// to an address the check has seen.
func (c *Client) guardedClient(policy netPolicy, originHost string) *http.Client {
	if c.allowPrivateHosts && c.injected != nil {
		// An explicitly injected client with the guard off: tests replace the
		// transport to describe their own network.
		return c.injected
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	guard := publicHostGuard{
		policy:     policy,
		originHost: strings.ToLower(strings.TrimSpace(originHost)),
		allowLocal: c.allowPrivateHosts,
		resolver:   c.resolver,
	}
	return newGuardedHTTPClient(guard, timeout)
}

// newGuardedHTTPClient binds one guard into a client: the transport dials only
// checked addresses and every redirect is checked before it is followed.
func newGuardedHTTPClient(guard publicHostGuard, timeout time.Duration) *http.Client {
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
			// A redirect is a new address and is checked like one: a page that
			// points into the local network is the same hole as a page that
			// lives there.
			return guard.checkURL(request.Context(), request.URL)
		},
	}
}
