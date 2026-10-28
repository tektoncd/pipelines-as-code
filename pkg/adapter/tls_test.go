package adapter

import (
	"path/filepath"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestIsTlsEnabled(t *testing.T) {
	secret := "pac-tls"
	tlsKey := "key"
	tlsCert := "cert"

	defer env.PatchAll(t, map[string]string{
		"SYSTEM_NAMESPACE": secret,
		"TLS_SECRET_NAME":  secret,
		"TLS_KEY":          tlsKey,
		"TLS_CERT":         tlsCert,
	})()

	tests := []struct {
		name   string
		secret *v1.Secret
		want   bool
	}{
		{
			name: "no secret",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{},
				Data:       map[string][]byte{},
			},
		},
		{
			name: "found secret",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secret,
					Namespace: secret,
				},
				Data: map[string][]byte{
					tlsCert: []byte("cert"),
					tlsKey:  []byte("key"),
				},
			},
			want: true,
		},
		{
			name: "missing key",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secret,
					Namespace: secret,
				},
				Data: map[string][]byte{
					tlsCert: []byte("abac"),
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*v1.Secret{
					tt.secret,
				},
			})
			l := listener{run: &params.Run{Clients: clients.Clients{Kube: cs.Kube}}}
			got, cert, key := l.isTLSEnabled()
			assert.Equal(t, got, tt.want)
			if got {
				assert.Equal(t, cert, filepath.Join(tlsMountPath, tlsCert))
				assert.Equal(t, key, filepath.Join(tlsMountPath, tlsKey))
			}
		})
	}
}
