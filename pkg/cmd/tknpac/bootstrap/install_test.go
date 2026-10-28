package bootstrap

import (
	"context"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestKubectlApplyNotFound(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	t.Setenv("PATH", "")
	err := kubectlApply(ctx, "some.yaml")
	assert.Assert(t, err != nil)
}

func TestUpdatePACConfigMap(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Kube: cs.Kube,
			Log:  log,
		},
	}
	opts := &bootstrapOpts{targetNamespace: "ns", dashboardURL: "https://dashboard.example.com"}

	// configmap does not exist yet, expect an error
	err := updatePACConfigMap(ctx, run, opts)
	assert.Assert(t, err != nil)

	_, err = run.Clients.Kube.CoreV1().ConfigMaps("ns").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code",
			Namespace: "ns",
		},
		Data: map[string]string{},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	err = updatePACConfigMap(ctx, run, opts)
	assert.NilError(t, err)

	cm, err := run.Clients.Kube.CoreV1().ConfigMaps("ns").Get(ctx, "pipelines-as-code", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, cm.Data["tekton-dashboard-url"], opts.dashboardURL)
}

func TestInstallGosmeeForwarderDeclined(t *testing.T) {
	origAskOne := prompt.SurveyAskOne
	defer func() { prompt.SurveyAskOne = origAskOne }()

	io, _ := newIOStream()
	opts := &bootstrapOpts{ioStreams: io, forwarderURL: "https://hook.example.com"}

	prompt.SurveyAskOne = func(_ survey.Prompt, response any, _ ...survey.AskOpt) error {
		if b, ok := response.(*bool); ok {
			*b = false
		}
		return nil
	}

	err := installGosmeeForwarder(context.Background(), opts)
	assert.Assert(t, err != nil)
}

func TestInstallPacNightlyKubectlMissing(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Kube: cs.Kube,
			Log:  log,
		},
	}
	io, _ := newIOStream()
	opts := &bootstrapOpts{
		ioStreams:       io,
		installNightly:  true,
		forceInstall:    true,
		targetNamespace: "ns",
	}
	t.Setenv("PATH", "")
	err := installPac(ctx, run, opts)
	assert.Assert(t, err != nil)
}
