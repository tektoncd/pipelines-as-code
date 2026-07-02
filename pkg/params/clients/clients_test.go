package clients

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestClientsGetURL(t *testing.T) {
	tests := []struct {
		name       string
		remoteURLS map[string]map[string]string
		want       string
		wantErr    bool
		url        string
	}{
		{
			name: "good",
			remoteURLS: map[string]map[string]string{
				"http://blahblah": {
					"body": "hellomoto",
					"code": "200",
				},
			},
			want: "hellomoto",
			url:  "http://blahblah",
		},
		{
			name: "bad",
			remoteURLS: map[string]map[string]string{
				"http://blahblah": {
					"code": "404",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.remoteURLS)
			c := &Clients{
				HTTP: *httpTestClient,
			}
			got, err := c.GetURL(ctx, tt.url)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err, "Clients.GetURL() error = %v, wantErr %v", err, tt.wantErr)
			assert.Equal(t, string(got), tt.want)
		})
	}
}

func TestClientsGetRemoteResourceURL(t *testing.T) {
	tests := []struct {
		name    string
		client  *http.Client
		opts    RemoteResourceFetchOptions
		url     string
		want    string
		wantErr string
	}{
		{
			name: "good public host",
			client: makeRemoteResourceHTTPSTestClient(t, map[string]map[string]string{
				"https://93.184.216.34/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:  "https://93.184.216.34/task.yaml",
			want: "task",
		},
		{
			name: "blocked plaintext http",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"http://example.com/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:     "http://example.com/task.yaml",
			wantErr: "scheme \"http\" is not allowed",
		},
		{
			name: "blocked plaintext http with bare allowlist host",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"http://93.184.216.34/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			opts: RemoteResourceFetchOptions{
				AllowedHosts: "93.184.216.34",
			},
			url:     "http://93.184.216.34/task.yaml",
			wantErr: "explicitly allowlisted with http://",
		},
		{
			name: "allowed plaintext http with scheme-qualified allowlist host",
			client: makeRemoteResourceHTTPTestClient(map[string]map[string]string{
				"http://93.184.216.34/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			opts: RemoteResourceFetchOptions{
				AllowedHosts: "http://93.184.216.34",
			},
			url:  "http://93.184.216.34/task.yaml",
			want: "task",
		},
		{
			name: "https blocked by http-only allowlist host",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://93.184.216.34/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			opts: RemoteResourceFetchOptions{
				AllowedHosts: "http://93.184.216.34",
			},
			url:     "https://93.184.216.34/task.yaml",
			wantErr: "not in the allowlist",
		},
		{
			name: "blocked loopback literal",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://127.0.0.1/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:     "https://127.0.0.1/task.yaml",
			wantErr: "loopback",
		},
		{
			name: "blocked loopback from dns",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://localhost/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:     "https://localhost/task.yaml",
			wantErr: "loopback",
		},
		{
			name: "blocked private literal",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://10.0.0.1/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:     "https://10.0.0.1/task.yaml",
			wantErr: "private",
		},
		{
			name: "blocked shared address space literal",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://100.64.0.1/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			url:     "https://100.64.0.1/task.yaml",
			wantErr: "shared address space",
		},
		{
			name: "blocked by allowlist",
			client: httptesthelper.MakeHTTPTestClient(map[string]map[string]string{
				"https://example.com/task.yaml": {
					"body": "task",
					"code": "200",
				},
			}),
			opts: RemoteResourceFetchOptions{
				AllowedHosts: "allowed.example.com",
			},
			url:     "https://example.com/task.yaml",
			wantErr: "not in the allowlist",
		},
		{
			name: "response too large",
			client: makeRemoteResourceHTTPSTestClient(t, map[string]map[string]string{
				"https://93.184.216.34/task.yaml": {
					"body": "123456",
					"code": "200",
				},
			}),
			opts: RemoteResourceFetchOptions{
				MaxResponseBytes: 5,
			},
			url:     "https://93.184.216.34/task.yaml",
			wantErr: "exceeds 5 bytes",
		},
		{
			name: "redirect to blocked host",
			client: makeRemoteResourceHTTPSTestClient(t, map[string]map[string]string{
				"https://93.184.216.34/task.yaml": {
					"code":     "302",
					"location": "http://169.254.169.254/latest/meta-data",
				},
			}),
			url:     "https://93.184.216.34/task.yaml",
			wantErr: "blocked redirect target",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			c := &Clients{
				HTTP: *tt.client,
			}
			got, err := c.GetRemoteResourceURL(ctx, tt.url, tt.opts)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, string(got), tt.want)
		})
	}
}

type remoteResourceHTTPTestConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (c remoteResourceHTTPTestConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

type remoteResourceHTTPTestAddr string

func (a remoteResourceHTTPTestAddr) Network() string {
	return "tcp"
}

func (a remoteResourceHTTPTestAddr) String() string {
	return string(a)
}

func makeRemoteResourceHTTPTestClient(config map[string]map[string]string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				clientConn, serverConn := net.Pipe()
				go serveRemoteResourceHTTPTestConn(serverConn, config)
				return remoteResourceHTTPTestConn{
					Conn:       clientConn,
					remoteAddr: remoteResourceHTTPTestAddr("93.184.216.34:80"),
				}, nil
			},
		},
	}
}

func makeRemoteResourceHTTPSTestClient(t *testing.T, config map[string]map[string]string) *http.Client {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		code, body, location := remoteResourceHTTPTestResponse(req, "https", config)
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	httpClient := server.Client()
	baseTransport, ok := httpClient.Transport.(*http.Transport)
	assert.Assert(t, ok)
	transport := baseTransport.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		if err != nil {
			return nil, err
		}
		return remoteResourceHTTPTestConn{
			Conn:       conn,
			remoteAddr: remoteResourceHTTPTestAddr("93.184.216.34:443"),
		}, nil
	}
	return &http.Client{
		Transport: transport,
	}
}

func serveRemoteResourceHTTPTestConn(conn net.Conn, config map[string]map[string]string) {
	defer conn.Close()

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	defer req.Body.Close()

	response := &http.Response{
		StatusCode: http.StatusNotFound,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}
	code, body, location := remoteResourceHTTPTestResponse(req, "http", config)
	response.StatusCode = code
	if body != "" {
		response.Body = io.NopCloser(bytes.NewBufferString(body))
	}
	if location != "" {
		response.Header.Set("Location", location)
	}
	_ = response.Write(conn)
}

func remoteResourceHTTPTestResponse(req *http.Request, scheme string, config map[string]map[string]string) (int, string, string) {
	values, ok := config[scheme+"://"+req.Host+req.URL.RequestURI()]
	if !ok {
		return http.StatusNotFound, "", ""
	}
	code, _ := strconv.Atoi(values["code"])
	return code, values["body"], values["location"]
}

func TestSafeRemoteResourceHTTPClientReplacesNonStandardTransport(t *testing.T) {
	policy, err := newRemoteResourcePolicy(RemoteResourceFetchOptions{})
	assert.NilError(t, err)

	mockTransport := httptesthelper.RoundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Request:    req,
		}
	})

	c := &Clients{
		HTTP: http.Client{
			Transport: mockTransport,
		},
	}

	httpClient := c.safeRemoteResourceHTTPClient(policy)
	transport, ok := httpClient.Transport.(*http.Transport)
	assert.Assert(t, ok, "non-standard transport should fall back to a guarded http.Transport")
	assert.Assert(t, transport.DialContext != nil)
}

func TestSafeRemoteResourceHTTPClientWrapsDefaultTransport(t *testing.T) {
	policy, err := newRemoteResourcePolicy(RemoteResourceFetchOptions{})
	assert.NilError(t, err)

	c := &Clients{
		HTTP: http.Client{},
	}

	httpClient := c.safeRemoteResourceHTTPClient(policy)
	transport, ok := httpClient.Transport.(*http.Transport)
	assert.Assert(t, ok, "nil transport should fall back to http.DefaultTransport and be wrapped")
	assert.Assert(t, transport.DialContext != nil)
}

func TestSafeRemoteResourceHTTPClientClearsTLSDialHooks(t *testing.T) {
	policy, err := newRemoteResourcePolicy(RemoteResourceFetchOptions{})
	assert.NilError(t, err)

	baseTransport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, nil
		},
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, nil
		},
		DialTLS: func(string, string) (net.Conn, error) {
			return nil, nil
		},
	}
	c := &Clients{
		HTTP: http.Client{
			Transport: baseTransport,
		},
	}

	httpClient := c.safeRemoteResourceHTTPClient(policy)
	transport, ok := httpClient.Transport.(*http.Transport)
	assert.Assert(t, ok)
	assert.Assert(t, transport.DialContext != nil)
	assert.Assert(t, transport.DialTLSContext == nil)
	assert.Assert(t, transport.DialTLS == nil) //nolint:staticcheck
	assert.Assert(t, baseTransport.DialTLSContext != nil)
	assert.Assert(t, baseTransport.DialTLS != nil) //nolint:staticcheck
}
