package vcshost

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name       string
		rawHost    string
		want       string
		wantErrSub string
	}{
		{
			name:       "empty host",
			wantErrSub: "hostname is empty",
		},
		{
			name:       "unparsable URL",
			rawHost:    "https://github.example.com/%zz",
			wantErrSub: "invalid hostname",
		},
		{
			name:       "missing host",
			rawHost:    "https://",
			wantErrSub: "invalid hostname",
		},
		{
			name:       "http scheme",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
		{
			name:       "userinfo is rejected",
			rawHost:    "https://token@github.example.com",
			wantErrSub: "must not contain credentials",
		},
		{
			name:       "query is rejected",
			rawHost:    "https://github.example.com?token=secret",
			wantErrSub: "must not contain credentials",
		},
		{
			name:       "fragment is rejected",
			rawHost:    "https://github.example.com#token",
			wantErrSub: "must not contain credentials",
		},
		{
			name:       "path is rejected",
			rawHost:    "https://github.example.com/owner",
			wantErrSub: "must not contain a path",
		},
		{
			name:    "bare hostname is accepted",
			rawHost: "github.example.com",
			want:    "github.example.com",
		},
		{
			name:    "normalizes case and trailing slash",
			rawHost: "https://GitHub.Example.COM/",
			want:    "github.example.com",
		},
		{
			name:    "surrounding spaces are trimmed",
			rawHost: "  github.example.com  ",
			want:    "github.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

func TestIsPublic(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "github.com", host: "github.com", want: true},
		{name: "api.github.com", host: "api.github.com", want: true},
		{name: "gitlab.com", host: "gitlab.com", want: true},
		{name: "bitbucket.org", host: "bitbucket.org", want: true},
		{name: "uppercase is still public", host: "GitHub.com", want: true},
		{name: "self hosted is not public", host: "github.example.com", want: false},
		{name: "lookalike is not public", host: "github.com.evil.example", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, IsPublic(tt.host), tt.want)
		})
	}
}

func TestCanonical(t *testing.T) {
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "api.github.com folds into github.com", host: "api.github.com", want: "github.com"},
		{name: "github.com is unchanged", host: "github.com", want: "github.com"},
		{name: "self hosted is lowercased", host: "GitHub.Example.COM", want: "github.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, Canonical(tt.host), tt.want)
		})
	}
}

func TestParseAllowlist(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		want       []string
		wantErrSub string
	}{
		{
			name: "empty value yields no host",
			raw:  "",
		},
		{
			name: "only separators yields no host",
			raw:  " , , ",
		},
		{
			name: "single host",
			raw:  "ghe.example.com",
			want: []string{"ghe.example.com"},
		},
		{
			name: "several hosts with spaces",
			raw:  "ghe.example.com, gitlab.example.com",
			want: []string{"ghe.example.com", "gitlab.example.com"},
		},
		{
			name: "https prefixes are normalized",
			raw:  "https://GHE.example.com",
			want: []string{"ghe.example.com"},
		},
		{
			name: "duplicates are collapsed",
			raw:  "ghe.example.com,GHE.EXAMPLE.COM",
			want: []string{"ghe.example.com"},
		},
		{
			name: "api.github.com is canonicalized",
			raw:  "api.github.com",
			want: []string{"github.com"},
		},
		{
			name:       "invalid entry is rejected",
			raw:        "ghe.example.com,https://evil.example.com/owner",
			wantErrSub: "invalid hostname",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllowlist(tt.raw)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		host      string
		want      bool
	}{
		{
			name:      "listed host",
			allowlist: []string{"ghe.example.com"},
			host:      "ghe.example.com",
			want:      true,
		},
		{
			name:      "case is ignored",
			allowlist: []string{"ghe.example.com"},
			host:      "GHE.Example.com",
			want:      true,
		},
		{
			name:      "api.github.com matches github.com",
			allowlist: []string{"github.com"},
			host:      "api.github.com",
			want:      true,
		},
		{
			name:      "unlisted host",
			allowlist: []string{"ghe.example.com"},
			host:      "attacker.example.com",
			want:      false,
		},
		{
			name:      "suffix lookalike does not match",
			allowlist: []string{"ghe.example.com"},
			host:      "evil-ghe.example.com",
			want:      false,
		},
		{
			name: "empty allowlist matches nothing",
			host: "ghe.example.com",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, Allowed(tt.allowlist, tt.host), tt.want)
		})
	}
}

func TestJoin(t *testing.T) {
	assert.Equal(t, Join([]string{"ghe.example.com", "gitlab.example.com"}), "ghe.example.com,gitlab.example.com")
	assert.Equal(t, Join(nil), "")
}

// Parse must map a hostname to the exact name a resolver would look up, so that
// the allowlist and DNS can never disagree about which host is being reached.
func TestParseNormalisation(t *testing.T) {
	tests := []struct {
		name    string
		rawHost string
		want    string
		wantErr bool
	}{
		{name: "uppercase scheme is accepted", rawHost: "HTTPS://GHE.Example.COM", want: "ghe.example.com"},
		{name: "uppercase host is lowercased", rawHost: "GHE.EXAMPLE.COM", want: "ghe.example.com"},
		{name: "root label trailing dot is dropped", rawHost: "ghe.example.com.", want: "ghe.example.com"},
		{name: "port is kept", rawHost: "ghe.example.com:8443", want: "ghe.example.com:8443"},
		{name: "unicode is mapped to punycode", rawHost: "ghé.example.com", want: "xn--gh-cja.example.com"},
		{
			// strings.ToLower would fold this onto plain "github.com" while
			// net/http dials xn--github-qyd.com, a registerable domain. Parse
			// must report the name that is really dialled.
			name:    "a turkish dotted capital I does not impersonate github.com",
			rawHost: "GİTHUB.com",
			want:    "xn--github-qyd.com",
		},
		{name: "an already punycoded host is stable", rawHost: "xn--gh-cja.example.com", want: "xn--gh-cja.example.com"},
		{
			name:    "unicode mapping cannot introduce a URL path",
			rawHost: "℀.com",
			wantErr: true,
		},
		{name: "http scheme is refused", rawHost: "http://ghe.example.com", wantErr: true},
		{name: "userinfo redirection is refused", rawHost: "https://ghe.example.com@evil.example", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.rawHost)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected %q to be refused, got %q", tt.rawHost, got)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

// A webhook payload must never be able to pin the controller to an address only
// reachable from inside the cluster or the cloud instance.
func TestIsPrivate(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "[::1]", want: true},
		{host: "169.254.169.254", want: true},
		{host: "10.0.0.1:8443", want: true},
		{host: "192.168.1.10", want: true},
		{host: "localhost", want: true},
		{host: "gitea", want: true},
		{host: "gitea.gitea.svc", want: true},
		{host: "gitea.gitea.svc.cluster.local", want: true},
		{host: "myservice.cluster.local", want: true},
		{host: "runner.internal", want: true},
		{host: "printer.local", want: true},
		{host: "gitea.home.arpa", want: true},
		{host: "127.1", want: true},
		{host: "0177.0.0.1", want: true},
		{host: "0x7f.1", want: true},
		{host: "192.168.1", want: true},
		{host: "github.com", want: false},
		{host: "ghe.example.com", want: false},
		{host: "140.82.121.4", want: false},
		{host: "1password.com", want: false},
		{host: "svc.example.com", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			assert.Equal(t, IsPrivate(tt.host), tt.want)
		})
	}
}

// Every provider Pipelines-as-Code supports must have its public instance
// listed, otherwise a stock install of that provider stops working the moment it
// goes through this gate.
//
// The provider list is read from the kubebuilder enum on Repository's provider
// type rather than hardcoded here, so that adding a provider without deciding
// what its public hostname is fails this test.
func TestIsPublicCoversEveryHostedProvider(t *testing.T) {
	publicInstanceOf := map[string]string{
		"github":          PublicGitHub,
		"gitlab":          PublicGitLab,
		"bitbucket-cloud": PublicBitbucket,
		"gitea":           PublicGitea,
		// Forgejo and Bitbucket Data Center have no single public instance:
		// codeberg.org is the well known Forgejo one and is listed on its own.
		"forgejo":              "",
		"bitbucket-datacenter": "",
	}

	for _, provider := range supportedProviderTypes(t) {
		host, known := publicInstanceOf[provider]
		assert.Assert(t, known,
			"provider %q has no entry in publicInstanceOf: add it here and, if it has a public instance, to publicHosts", provider)
		if host == "" {
			continue
		}
		assert.Assert(t, IsPublic(host), "%s should be a known public instance", host)
	}

	// Hostnames that are not a provider type of their own but must still be known.
	for _, host := range []string{"api.github.com", "api.bitbucket.org", PublicCodeberg} {
		assert.Assert(t, IsPublic(host), "%s should be a known public instance", host)
	}
	assert.Equal(t, Canonical("api.bitbucket.org"), PublicBitbucket)
}

// supportedProviderTypes reads the provider types from the kubebuilder
// validation enum that defines them on the Repository CRD.
func supportedProviderTypes(t *testing.T) []string {
	t.Helper()
	const typesFile = "../apis/pipelinesascode/v1alpha1/types.go"
	source, err := os.ReadFile(typesFile)
	assert.NilError(t, err)
	// The Type field of GitProvider is the one enum listing "github".
	pattern := regexp.MustCompile(`\+kubebuilder:validation:Enum=([a-z;-]*\bgithub\b[a-z;-]*)\s*\n\s*Type string`)
	match := pattern.FindSubmatch(source)
	assert.Assert(t, match != nil, "no git provider enum found in %s", typesFile)
	providers := strings.Split(string(match[1]), ";")
	assert.Assert(t, len(providers) > 1, "provider enum in %s looks wrong: %v", typesFile, providers)
	return providers
}
