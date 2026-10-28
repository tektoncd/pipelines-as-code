package kubeinteraction

import (
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestDeleteSecret(t *testing.T) {
	ns := "there"

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			},
		},
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
					Name:      "pac-git-basic-auth-owner-repo",
				},
				StringData: map[string]string{
					".git-credentials": "https://whateveryousayboss",
				},
			},
		},
	}

	tests := []struct {
		name string
	}{
		{
			name: "auth basic secret there",
		},
		{
			name: "auth basic secret not there",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			kint := Interaction{
				Run: &params.Run{
					Clients: clients.Clients{
						Kube: stdata.Kube,
					},
				},
			}
			err := kint.DeleteSecret(ctx, fakelogger, "", ns)
			assert.NilError(t, err)
		})
	}
}

func TestUpdateSecretWithOwnerRef(t *testing.T) {
	testNs := "there"
	secrete := "dont-tell-anyone-its-a-secrete"
	tdata := testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNs,
					Name:      secrete,
				},
			},
		},
	}
	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	kint := Interaction{
		Run: &params.Run{
			Clients: clients.Clients{
				Kube: stdata.Kube,
			},
		},
	}
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "uid",
		},
	}

	err := kint.UpdateSecretWithOwnerRef(ctx, fakelogger, testNs, secrete, pr)
	assert.NilError(t, err)

	updatedSecret, err := stdata.Kube.CoreV1().Secrets(testNs).Get(ctx, secrete, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(updatedSecret.OwnerReferences) != 0)
	assert.Equal(t, updatedSecret.OwnerReferences[0].Kind, "PipelineRun")
	assert.Equal(t, updatedSecret.OwnerReferences[0].Name, pr.Name)
}
