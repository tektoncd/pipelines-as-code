package clients

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

type RemoteResourceFetchOptions struct {
	AllowedHosts     string
	BlockedCIDRs     string
	MaxResponseBytes int64
}

type remoteResourcePolicy struct {
	allowedHTTPSHosts map[string]bool
	allowedHTTPHosts  map[string]bool
	hasAllowlist      bool
	blockedCIDRs      []netip.Prefix
	maxBytes          int64
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func (c *Clients) GetRemoteResourceURL(ctx context.Context, rawURL string, opts RemoteResourceFetchOptions) ([]byte, error) {
	policy, err := validateRemoteResourceURL(ctx, rawURL, opts)
	if err != nil {
		return nil, err
	}

	nctx, cancel := context.WithTimeout(ctx, RequestMaxWaitTime)
	defer cancel()

	req, err := http.NewRequestWithContext(nctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	httpClient := c.safeRemoteResourceHTTPClient(policy)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	statusOK := res.StatusCode >= 200 && res.StatusCode < 300
	if !statusOK {
		return nil, fmt.Errorf("Non-OK HTTP status: %d", res.StatusCode)
	}

	limitedBody := &io.LimitedReader{R: res.Body, N: policy.maxBytes + 1}
	data, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > policy.maxBytes {
		return nil, fmt.Errorf("remote resource response exceeds %d bytes", policy.maxBytes)
	}
	return data, nil
}

// ValidateRemoteResourceURL checks whether a remote resource URL is allowed by
// the same policy used by GetRemoteResourceURL, without fetching its content.
func ValidateRemoteResourceURL(ctx context.Context, rawURL string, opts RemoteResourceFetchOptions) error {
	_, err := validateRemoteResourceURL(ctx, rawURL, opts)
	return err
}

// ValidateRemoteResourceProviderURL checks whether a provider-backed remote
// resource URL is allowed without resolving the URL host. Provider-backed
// fetches use the provider API instead of dialing the annotation URL directly.
func ValidateRemoteResourceProviderURL(rawURL string, opts RemoteResourceFetchOptions) error {
	policy, err := newRemoteResourcePolicy(opts)
	if err != nil {
		return err
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	return policy.validateURL(parsedURL)
}

// CheckRemoteResourceResponseSize checks a response size against the configured
// remote resource limit.
func CheckRemoteResourceResponseSize(responseSize int64, opts RemoteResourceFetchOptions) error {
	policy, err := newRemoteResourcePolicy(opts)
	if err != nil {
		return err
	}
	if responseSize > policy.maxBytes {
		return fmt.Errorf("remote resource response exceeds %d bytes", policy.maxBytes)
	}
	return nil
}

func validateRemoteResourceURL(ctx context.Context, rawURL string, opts RemoteResourceFetchOptions) (remoteResourcePolicy, error) {
	policy, err := newRemoteResourcePolicy(opts)
	if err != nil {
		return policy, err
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return policy, err
	}
	if err := policy.validateURL(parsedURL); err != nil {
		return policy, err
	}
	if err := policy.validateResolvedHost(ctx, parsedURL.Hostname()); err != nil {
		return policy, err
	}
	return policy, nil
}

func (c *Clients) safeRemoteResourceHTTPClient(policy remoteResourcePolicy) http.Client {
	httpClient := c.HTTP
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if err := policy.validateURL(req.URL); err != nil {
			return fmt.Errorf("blocked redirect target: %w", err)
		}
		return nil
	}

	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	baseTransport, ok := transport.(*http.Transport)
	if !ok {
		defaultTransport, defaultOK := http.DefaultTransport.(*http.Transport)
		if !defaultOK {
			defaultTransport = &http.Transport{}
		}
		baseTransport = defaultTransport
	}

	safeTransport := baseTransport.Clone()
	originalDialContext := safeTransport.DialContext
	if originalDialContext == nil {
		originalDialContext = (&net.Dialer{Timeout: ConnectMaxWaitTime}).DialContext
	}
	safeTransport.DialTLSContext = nil
	safeTransport.DialTLS = nil //nolint:staticcheck // Clear deprecated hook so HTTPS cannot bypass DialContext.
	safeTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if err := policy.validateResolvedHost(ctx, host); err != nil {
			return nil, err
		}

		conn, err := originalDialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if err := policy.validateRemoteAddr(conn.RemoteAddr()); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
	httpClient.Transport = safeTransport
	return httpClient
}

func newRemoteResourcePolicy(opts RemoteResourceFetchOptions) (remoteResourcePolicy, error) {
	allowedHTTPSHosts, allowedHTTPHosts, hasAllowlist := parseAllowedHostList(opts.AllowedHosts)
	policy := remoteResourcePolicy{
		allowedHTTPSHosts: allowedHTTPSHosts,
		allowedHTTPHosts:  allowedHTTPHosts,
		hasAllowlist:      hasAllowlist,
		maxBytes:          opts.MaxResponseBytes,
	}
	if policy.maxBytes <= 0 {
		policy.maxBytes = DefaultRemoteResourceMaxResponseBytes
	}

	blockedCIDRs, err := parseCIDRList(opts.BlockedCIDRs)
	if err != nil {
		return policy, err
	}
	policy.blockedCIDRs = blockedCIDRs
	return policy, nil
}

func (p remoteResourcePolicy) validateURL(parsedURL *url.URL) error {
	if parsedURL == nil {
		return fmt.Errorf("remote resource URL is empty")
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("remote resource URL scheme %q is not allowed", parsedURL.Scheme)
	}
	host := parsedURL.Hostname()
	if host == "" {
		return fmt.Errorf("remote resource URL host is empty")
	}

	switch {
	case scheme == "http" && !p.isAllowedHost(host, p.allowedHTTPHosts):
		return fmt.Errorf("remote resource URL scheme %q is not allowed unless host %q is explicitly allowlisted with http://", scheme, host)
	case scheme == "https" && p.hasAllowlist && !p.isAllowedHost(host, p.allowedHTTPSHosts):
		return fmt.Errorf("remote resource URL host %q is not in the allowlist", host)
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		return p.validateAddr(addr)
	}
	return nil
}

func (p remoteResourcePolicy) isAllowedHost(host string, allowedHosts map[string]bool) bool {
	host = normalizeHost(host)
	if allowedHosts[host] {
		return true
	}
	for allowedHost := range allowedHosts {
		if strings.HasPrefix(allowedHost, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowedHost, "*")) {
			return true
		}
	}
	return false
}

func (p remoteResourcePolicy) validateResolvedHost(ctx context.Context, host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		return p.validateAddr(addr)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return fmt.Errorf("cannot parse resolved IP address %q", ip.IP.String())
		}
		if err := p.validateAddr(addr); err != nil {
			return err
		}
	}
	return nil
}

func (p remoteResourcePolicy) validateRemoteAddr(remoteAddr net.Addr) error {
	if remoteAddr == nil {
		return fmt.Errorf("remote address is empty")
	}
	host, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	return p.validateAddr(addr)
}

func (p remoteResourcePolicy) validateAddr(addr netip.Addr) error {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid():
		return fmt.Errorf("remote resource address is invalid")
	case addr.IsUnspecified():
		return fmt.Errorf("remote resource address %s is unspecified", addr)
	case addr.IsLoopback():
		return fmt.Errorf("remote resource address %s is loopback", addr)
	case addr.IsPrivate():
		return fmt.Errorf("remote resource address %s is private", addr)
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return fmt.Errorf("remote resource address %s is link-local", addr)
	case addr.IsMulticast():
		return fmt.Errorf("remote resource address %s is multicast", addr)
	case cgnatPrefix.Contains(addr):
		return fmt.Errorf("remote resource address %s is shared address space", addr)
	}
	for _, prefix := range p.blockedCIDRs {
		if prefix.Contains(addr) {
			return fmt.Errorf("remote resource address %s is in blocked CIDR %s", addr, prefix)
		}
	}
	return nil
}

func parseAllowedHostList(value string) (map[string]bool, map[string]bool, bool) {
	httpsHosts := map[string]bool{}
	httpHosts := map[string]bool{}
	hasAllowlist := false
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		hasAllowlist = true
		scheme := "https"
		host := item
		if strings.Contains(item, "://") {
			if parsedURL, err := url.Parse(item); err == nil {
				scheme = strings.ToLower(parsedURL.Scheme)
				host = parsedURL.Host
			}
		}
		host = normalizeHost(host)
		if host == "" {
			continue
		}
		switch scheme {
		case "http":
			httpHosts[host] = true
		case "https":
			httpsHosts[host] = true
		}
	}
	return httpsHosts, httpHosts, hasAllowlist
}

func parseCIDRList(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return nil, fmt.Errorf("invalid blocked CIDR %q: %w", item, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		if parsedURL, err := url.Parse(host); err == nil {
			host = parsedURL.Host
		}
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return strings.TrimSuffix(host, ".")
}
