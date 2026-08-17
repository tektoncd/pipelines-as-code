//go:build e2e

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The trusted provider hostname policy is controller wide, so these tests patch
// the ConfigMap the GHE controller reads and must not run in parallel with each
// other or with any other GHE test.
const gheControllerName = "ghe-controller"

// TestGithubGHETrustOnFirstUse checks that a controller with no configured
// allowlist records the GHE hostname of a webhook GitHub itself signed, and
// keeps serving it.
//
// The unit tests cover the decision; what only a cluster can tell us is whether
// the controller is actually allowed to write to its own ConfigMap. Without the
// RBAC grant that ships with this policy every first webhook from a self hosted
// instance fails, and nothing else in the suite would notice.
func TestGithubGHETrustOnFirstUse(t *testing.T) {
	ctx := context.Background()
	onGHE := true
	ctx, runcnx, _, ghprovider, err := tgithub.Setup(ctx, onGHE, false)
	assert.NilError(t, err)

	configMapName := tgithub.ControllerConfigMapName(onGHE)
	host := configmap.ProviderHost(t, *ghprovider.APIURL)

	// Hand the policy back to the controller, so that it has to learn the host
	// again rather than find it already trusted.
	defer configmap.ResetTrustedProviderHostnames(ctx, t, runcnx, configMapName)()
	runcnx.Clients.Log.Infof("Cleared the trusted provider hostnames of %s, expecting the controller to learn %s",
		configMapName, host)

	g := &tgithub.PRTest{
		Label:     "Github trust on first use",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// The write happens while the webhook is served, so it has already landed by
	// the time the PipelineRun succeeded. Poll anyway: the controller retries the
	// update on conflict, and asserting once would flake on that retry.
	var allowlist, autoTrusted []string
	for range 12 {
		allowlist, autoTrusted = configmap.TrustedProviderHostnames(ctx, t, runcnx, configMapName)
		if slices.Contains(autoTrusted, host) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.Assert(t, !slices.Contains(allowlist, host),
		"the controller wrote learned host %s into the administrator-owned %q key of the %s ConfigMap, got %v",
		host, settings.TrustedProviderHostnamesKey, configMapName, allowlist)
	assert.Assert(t, slices.Contains(autoTrusted, host),
		"the controller did not record %s in the %s annotation of the %s ConfigMap, got %v. "+
			"A missing update permission on its own ConfigMap looks exactly like this",
		host, keys.AutoTrustedProviderHostnames, configMapName, autoTrusted)
}

// TestGithubGHEUntrustedHostRefused checks that an administrator configured
// allowlist which leaves the GHE host out stops the controller from using its
// credentials against it.
//
// It drives an incoming webhook, which carries no provider signature and can
// therefore never be trusted on first use, so the refusal is the policy talking
// rather than a missing signature. Only a cluster can tell us the gate really
// sits on the serving path.
func TestGithubGHEUntrustedHostRefused(t *testing.T) {
	ctx := context.Background()
	onGHE := true
	ctx, runcnx, opts, ghprovider, err := tgithub.Setup(ctx, onGHE, false)
	assert.NilError(t, err)

	configMapName := tgithub.ControllerConfigMapName(onGHE)
	host := configmap.ProviderHost(t, *ghprovider.APIURL)
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	repoinfo, resp, err := ghprovider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, targetNS, targetNS, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	incoming := &[]v1alpha1.Incoming{
		{
			Type:    "webhook-url",
			Secret:  v1alpha1.Secret{Name: incomingSecretName, Key: "incoming"},
			Targets: []string{targetNS},
			Params:  []string{"the_best_superhero_is"},
		},
	}
	assert.NilError(t, tgithub.CreateCRDIncoming(ctx, t, repoinfo, runcnx, incoming, opts, ghprovider, targetNS))
	assert.NilError(t, secret.Create(ctx, runcnx, map[string]string{"incoming": incomingSecreteValue}, targetNS, incomingSecretName))

	targetRefName := "refs/heads/" + targetNS
	title := "TestGithubGHEUntrustedHostRefused - " + targetNS
	sha, vref, err := tgithub.PushFilesToRef(ctx, ghprovider.Client(), title,
		repoinfo.GetDefaultBranch(), targetRefName, opts.Organization, opts.Repo, entries)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Commit %s has been created and pushed to branch %s", sha, vref.GetURL())

	g := tgithub.PRTest{
		Cnx:             runcnx,
		Options:         opts,
		Provider:        ghprovider,
		TargetNamespace: targetNS,
		TargetRefName:   targetRefName,
		PRNumber:        -1,
		SHA:             sha,
		Logger:          runcnx.Clients.Log,
		GHE:             onGHE,
	}
	defer g.TearDown(ctx, t)

	// A non-empty administrator list is authoritative. The GHE host is
	// deliberately left out.
	defer configmap.ChangeGlobalConfig(ctx, t, runcnx, configMapName, map[string]string{
		settings.TrustedProviderHostnamesKey: "github.com",
	})()
	runcnx.Clients.Log.Infof("Set an authoritative allowlist on %s that leaves %s out", configMapName, host)

	incomingURL := fmt.Sprintf("%s/incoming", opts.ControllerURL)
	jsonData, err := json.Marshal(map[string]any{
		"repository":  targetNS,
		"branch":      targetNS,
		"pipelinerun": "pipelinerun-incoming",
		"secret":      incomingSecreteValue,
		"params":      map[string]string{"the_best_superhero_is": "Superman"},
	})
	assert.NilError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(string(jsonData)))
	assert.NilError(t, err)
	req.Header.Add("Content-Type", "application/json")

	httpResp, err := (&http.Client{}).Do(req)
	assert.NilError(t, err)
	defer httpResp.Body.Close()
	runcnx.Clients.Log.Infof("Sent an incoming request to %s for a host that is not trusted", incomingURL)

	numLines := int64(300)
	sinceSeconds := int64(300)
	// The controller logs JSON, so the quotes around the hostname reach the log
	// line escaped. Accept both shapes rather than depend on the encoder.
	refusal := regexp.MustCompile(
		`refusing to use credentials with the \\?"` + regexp.QuoteMeta(host) + `\\?" host`,
	)
	assert.NilError(t, twait.RegexpMatchingInControllerLog(ctx, runcnx, *refusal, 10, gheControllerName, &numLines, &sinceSeconds),
		"the controller did not refuse the untrusted host, which means the allowlist is not gating the serving path")

	prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, sha),
	})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) == 0,
		"a PipelineRun was created for a provider host the allowlist refuses, got %d", len(prs.Items))
}
