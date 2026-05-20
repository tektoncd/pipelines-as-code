// Package tlsconfig provides TLS configuration parsing and management for the API server.
// It supports loading configuration from environment variables or explicit values,
// and converts them to Go's tls.Config for use with HTTPS servers.
package tlsconfig

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Config holds TLS configuration that can be loaded from environment variables.
type Config struct {
	MinTLSVersion    string // e.g., "1.2", "1.3"
	CipherSuites     string // Comma-separated list of IANA cipher suite names or numeric IDs
	CurvePreferences string // Comma-separated list of curve names (e.g., "X25519,P256")
}

// New creates a TLS Config from explicit values.
func New(minVersion, cipherSuites, curvePreferences string) *Config {
	return &Config{
		MinTLSVersion:    minVersion,
		CipherSuites:     cipherSuites,
		CurvePreferences: curvePreferences,
	}
}

// LoadFromEnv loads TLS configuration from environment variables
// This allows the configuration to be provided via ConfigMap in Kubernetes
// or injected by the Tekton operator on OpenShift.
func LoadFromEnv(getenv func(string) string) *Config {
	return New(getenv("TLS_MIN_VERSION"), getenv("TLS_CIPHER_SUITES"), getenv("TLS_CURVE_PREFERENCES"))
}

// ToTLSConfig converts the configuration to Go's tls.Config.
func (c *Config) ToTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		// NextProtos enables ALPN (Application-Layer Protocol Negotiation) for HTTP/2 support
		NextProtos: []string{"h2", "http/1.1"},
		// MinVersion explicitly set to TLS 1.2 as the secure default
		MinVersion: tls.VersionTLS12,
	}

	// Override minimum TLS version if specified
	if c.MinTLSVersion != "" {
		minVersion, err := parseTLSVersion(c.MinTLSVersion)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS_MIN_VERSION: %w", err)
		}
		tlsConfig.MinVersion = minVersion
	}

	// Set cipher suites
	if c.CipherSuites != "" {
		cipherSuites, err := parseCipherSuites(c.CipherSuites)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS_CIPHER_SUITES: %w", err)
		}
		tlsConfig.CipherSuites = cipherSuites
	}

	// Set curve preferences (for PQC support)
	if c.CurvePreferences != "" {
		curvePreferences, err := parseCurvePreferences(c.CurvePreferences)
		if err != nil {
			return nil, fmt.Errorf("invalid TLS_CURVE_PREFERENCES: %w", err)
		}
		tlsConfig.CurvePreferences = curvePreferences
	}

	return tlsConfig, nil
}

// parseTLSVersion converts a version string to tls.Version constant
// Supports: "1.0", "1.1", "1.2", "1.3" or numeric values like "772" (0x0304).
func parseTLSVersion(version string) (uint16, error) {
	// Try parsing as decimal number first (e.g., "772" for TLS 1.3)
	if num, err := strconv.ParseUint(version, 10, 16); err == nil {
		v := uint16(num)
		if v >= tls.VersionTLS10 && v <= tls.VersionTLS13 {
			return v, nil
		}
		return 0, fmt.Errorf("unsupported numeric TLS version: %d", v)
	}

	// Parse as version string
	switch version {
	case "1.0", "TLS1.0", "TLSv1.0":
		return tls.VersionTLS10, nil
	case "1.1", "TLS1.1", "TLSv1.1":
		return tls.VersionTLS11, nil
	case "1.2", "TLS1.2", "TLSv1.2":
		return tls.VersionTLS12, nil
	case "1.3", "TLS1.3", "TLSv1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown TLS version: %s (supported: 1.0, 1.1, 1.2, 1.3)", version)
	}
}

// cipherSuiteCache caches the mapping from cipher name to ID
// Built from Go's tls.CipherSuites() (secure ciphers only).
var (
	cipherSuiteCache     map[string]uint16
	cipherSuiteCacheOnce sync.Once
)

// getCipherSuiteMap returns a map of IANA cipher suite names to their IDs
// Uses Go's built-in tls.CipherSuites() which contains only secure ciphers.
// Note: Insecure ciphers (tls.InsecureCipherSuites()) are intentionally excluded.
// If required to support them in the future (hopefully not), add:
//
//	for _, suite := range tls.InsecureCipherSuites() {
//		cipherSuiteCache[suite.Name] = suite.ID
//	}
func getCipherSuiteMap() map[string]uint16 {
	cipherSuiteCacheOnce.Do(func() {
		cipherSuiteCache = make(map[string]uint16)
		for _, suite := range tls.CipherSuites() {
			cipherSuiteCache[suite.Name] = suite.ID
		}
	})
	return cipherSuiteCache
}

// parseCipherSuites parses a comma-separated list of cipher suites
// Supports IANA cipher names (e.g., "TLS_AES_128_GCM_SHA256") and numeric IDs (e.g., "4865")
// The cipher names are looked up using Go's built-in tls.CipherSuites()
// Unknown or insecure cipher names are silently skipped; the resulting configuration
// is logged at startup so users can verify which ciphers were applied.
func parseCipherSuites(ciphers string) ([]uint16, error) {
	if ciphers == "" {
		return nil, nil
	}

	cipherMap := getCipherSuiteMap()
	parts := strings.Split(ciphers, ",")
	result := make([]uint16, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Try parsing as numeric ID first
		if id, err := strconv.ParseUint(part, 10, 16); err == nil {
			uID := uint16(id)
			// Ensure the numeric ID corresponds to a known secure cipher suite
			isSecure := false
			for _, suite := range tls.CipherSuites() {
				if suite.ID == uID {
					isSecure = true
					break
				}
			}
			if !isSecure {
				return nil, fmt.Errorf("unknown or insecure cipher suite ID: %d", uID)
			}
			result = append(result, uID)
			continue
		}

		// Try looking up by IANA name using Go's built-in cipher suite info
		if id, ok := cipherMap[part]; ok {
			result = append(result, id)
			continue
		}

		// Unknown or insecure cipher - silently skip
		// The resulting config is logged at startup for verification
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid cipher suites found in: %s", ciphers)
	}

	return result, nil
}

// curveNameToID maps curve names to their tls.CurveID values.
//
// References:
//   - IANA TLS Supported Groups Registry:
//     https://www.iana.org/assignments/tls-parameters/tls-parameters.xhtml#tls-parameters-8
//   - Go crypto/tls CurveID constants:
//     https://pkg.go.dev/crypto/tls#CurveID
//   - RFC 8446 Section 4.2.7 (TLS 1.3 Supported Groups):
//     https://datatracker.ietf.org/doc/html/rfc8446#section-4.2.7
//   - Post-Quantum Key Agreement (X25519Kyber768Draft00):
//     https://datatracker.ietf.org/doc/draft-ietf-tls-hybrid-design/
//     IANA ID: 25497 (0x6399)
var curveNameToID = map[string]tls.CurveID{
	// NIST curves (IANA IDs from RFC 8446)
	"P256":      tls.CurveP256, // IANA ID: 23 (secp256r1)
	"P384":      tls.CurveP384, // IANA ID: 24 (secp384r1)
	"P521":      tls.CurveP521, // IANA ID: 25 (secp521r1)
	"P-256":     tls.CurveP256, // Alias
	"P-384":     tls.CurveP384, // Alias
	"P-521":     tls.CurveP521, // Alias
	"CurveP256": tls.CurveP256, // Alias
	"CurveP384": tls.CurveP384, // Alias
	"CurveP521": tls.CurveP521, // Alias

	// Modern curves
	"X25519": tls.X25519, // IANA ID: 29

	// Post-Quantum Cryptography (PQC) hybrid curve
	// X25519Kyber768Draft00 combines X25519 with Kyber768 for quantum-resistant key exchange
	// Reference: https://datatracker.ietf.org/doc/draft-ietf-tls-hybrid-design/
	// IANA ID: 25497 (0x6399) - registered as part of the hybrid PQ key exchange draft
	// Note: This may not be available as a Go constant in all versions.
	// When Go adds native support, update this to use tls.X25519Kyber768Draft00
	"X25519Kyber768Draft00": tls.CurveID(25497),
}

// parseCurvePreferences parses a comma-separated list of curve names
// Supports curve names (e.g., "X25519", "P256") and numeric IDs
// Unknown curve names are silently skipped; the resulting configuration
// is logged at startup so users can verify which curves were applied.
func parseCurvePreferences(curves string) ([]tls.CurveID, error) {
	if curves == "" {
		return nil, nil
	}

	parts := strings.Split(curves, ",")
	result := make([]tls.CurveID, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Try parsing as numeric ID first
		if id, err := strconv.ParseUint(part, 10, 16); err == nil {
			cID := tls.CurveID(id)
			// Check if it's a known curve
			isValid := false
			for _, cid := range curveNameToID {
				if cid == cID {
					isValid = true
					break
				}
			}
			if !isValid {
				return nil, fmt.Errorf("unknown curve ID: %d", cID)
			}
			result = append(result, cID)
			continue
		}

		// Try parsing as curve name
		if id, ok := curveNameToID[part]; ok {
			result = append(result, id)
			continue
		}

		// Unknown curve - silently skip
		// The resulting config is logged at startup for verification
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid curve preferences found in: %s", curves)
	}

	return result, nil
}

// GetTLSVersionName returns a human-readable name for a TLS version.
func GetTLSVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	case 0:
		return "default"
	default:
		return fmt.Sprintf("Unknown (0x%04x)", version)
	}
}

// GetCipherSuiteName returns the name of a cipher suite given its ID.
// Uses Go's built-in cipher suite info.
func GetCipherSuiteName(id uint16) string {
	for _, suite := range tls.CipherSuites() {
		if suite.ID == id {
			return suite.Name
		}
	}
	for _, suite := range tls.InsecureCipherSuites() {
		if suite.ID == id {
			return suite.Name
		}
	}
	return fmt.Sprintf("Unknown (0x%04x)", id)
}

// curveIDToName provides deterministic name lookup for curves,
// avoiding map iteration order issues with curveNameToID.
var curveIDToName = map[tls.CurveID]string{
	tls.CurveP256:      "P256",
	tls.CurveP384:      "P384",
	tls.CurveP521:      "P521",
	tls.X25519:         "X25519",
	tls.CurveID(25497): "X25519Kyber768Draft00",
}

// GetCurveName returns the name of a curve given its ID.
func GetCurveName(id tls.CurveID) string {
	if name, ok := curveIDToName[id]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (0x%04x)", uint16(id))
}

// FormatCipherSuites returns a human-readable string of cipher suite names.
// Returns "default" if the slice is empty (Go's defaults will be used).
func FormatCipherSuites(ciphers []uint16) string {
	if len(ciphers) == 0 {
		return "default"
	}
	names := make([]string, len(ciphers))
	for i, id := range ciphers {
		names[i] = GetCipherSuiteName(id)
	}
	return strings.Join(names, ", ")
}

// FormatCurvePreferences returns a human-readable string of curve names.
// Returns "default" if the slice is empty (Go's defaults will be used).
func FormatCurvePreferences(curves []tls.CurveID) string {
	if len(curves) == 0 {
		return "default"
	}
	names := make([]string, len(curves))
	for i, id := range curves {
		names[i] = GetCurveName(id)
	}
	return strings.Join(names, ", ")
}
