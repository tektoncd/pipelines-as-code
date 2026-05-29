package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// NewTestClient returns *http.Client with Transport replaced to avoid making real calls.
func newHTTPTestClient(fn RoundTripFunc) *http.Client {
	return &http.Client{
		Transport: fn,
	}
}

// MakeHTTPTestClient creates a test HTTP client from a config map.
func MakeHTTPTestClient(config map[string]map[string]string) *http.Client {
	return newHTTPTestClient(func(req *http.Request) *http.Response {
		resp := &http.Response{}
		for k, v := range config {
			if k == req.URL.String() {
				code, _ := strconv.Atoi(v["code"])
				resp = &http.Response{
					StatusCode: code,
					Header:     make(http.Header),
				}
				if body, ok := v["body"]; ok {
					resp.Body = io.NopCloser(bytes.NewBufferString(body))
				}
			}
		}
		return resp
	})
}

// MakeHTTPTransportTestClient creates a test HTTP client backed by a real
// *http.Transport, while routing all dials to local test servers.
func MakeHTTPTransportTestClient(t testing.TB, config map[string]map[string]string) *http.Client {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		scheme := "http"
		if req.TLS != nil {
			scheme = "https"
		}
		values, ok := config[scheme+"://"+req.Host+req.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		code, _ := strconv.Atoi(values["code"])
		w.WriteHeader(code)
		if body, ok := values["body"]; ok {
			_, _ = io.WriteString(w, body)
		}
	})

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	httpsServer := httptest.NewTLSServer(handler)
	t.Cleanup(httpsServer.Close)

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				if port == "443" {
					return dialHTTPTransportTestConn(ctx, network, address, httpsServer.Listener.Addr().String())
				}
				return dialHTTPTransportTestConn(ctx, network, address, httpServer.Listener.Addr().String())
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
}

func dialHTTPTransportTestConn(ctx context.Context, network, remoteAddress, testServerAddress string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, testServerAddress)
	if err != nil {
		return nil, err
	}
	return httpTransportTestConn{
		Conn:       conn,
		remoteAddr: httpTransportTestAddr(remoteAddress),
	}, nil
}

type httpTransportTestConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (c httpTransportTestConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

type httpTransportTestAddr string

func (a httpTransportTestAddr) Network() string {
	return "tcp"
}

func (a httpTransportTestAddr) String() string {
	return string(a)
}

// RoundTripFunc is a function adapter to implement http.RoundTripper interface.
type RoundTripFunc func(req *http.Request) *http.Response

// RoundTrip implements the http.RoundTripper interface.
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}
