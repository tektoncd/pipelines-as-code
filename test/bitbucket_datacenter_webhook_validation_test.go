//go:build e2e

package test

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	bbv1test "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/test"
	tbbdc "github.com/openshift-pipelines/pipelines-as-code/test/pkg/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// verifyWebhookRejection asserts that the controller logged the expected rejection
// reason for a webhook that should have been refused, and that no PipelineRun was
// created as a result.
func verifyWebhookRejection(ctx context.Context, t *testing.T, runcnx *params.Run, namespace, rejectionReason string) {
	t.Helper()

	numLines := int64(300)
	sinceSeconds := int64(120)
	reg := regexp.MustCompile(regexp.QuoteMeta(rejectionReason))
	err := twait.RegexpMatchingInControllerLog(ctx, runcnx, *reg, 10, "controller", &numLines, &sinceSeconds)
	assert.NilError(t, err, "expected to find rejection reason %q in controller logs", rejectionReason)

	prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(prs.Items), "Expected no PipelineRuns but found %d", len(prs.Items))
}

// TestBitbucketDataCenterCloneURLValidation verifies the provider refuses a pull
// request whose clone URL host differs from the repository URL, and creates no
// PipelineRun for it.
func TestBitbucketDataCenterCloneURLValidation(t *testing.T) {
	webhookSecret := os.Getenv("TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET")
	if webhookSecret == "" {
		t.Fatal("TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET environment variable is required")
	}

	elURL := os.Getenv("TEST_EL_URL")
	if elURL == "" {
		t.Fatal("TEST_EL_URL environment variable is required")
	}

	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")
	if bitbucketWSOwner == "" {
		t.Fatal("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY environment variable is required")
	}

	tests := []struct {
		name            string
		cloneURL        string
		rejectionReason string
	}{
		{
			name:            "clone URL on a different host",
			cloneURL:        "https://other.example.com/scm/PROJ/repo.git",
			rejectionReason: "does not match the host of repository URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-sec")

			ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
			assert.NilError(t, err)

			ctx, err = cctx.GetControllerCtxInfo(ctx, runcnx)
			assert.NilError(t, err)

			repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
			defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

			// Match the Repository on repo.Link; only the clone URL differs.
			ev := info.NewEvent()
			ev.Organization = opts.Organization
			ev.Repository = opts.Repo
			ev.URL = repo.Link
			ev.HeadURL = repo.Link
			ev.CloneURL = tt.cloneURL
			ev.SHA = "abc123def456"
			ev.AccountID = "1"
			ev.Sender = "pac"

			payloadBytes, err := json.Marshal(bbv1test.MakePREvent(ev, ""))
			assert.NilError(t, err)
			signature := tbbdc.GenerateHMAC(payloadBytes, webhookSecret)

			resp, err := tbbdc.SendWebhook(ctx, t, elURL, payloadBytes, signature, "pr:opened")
			assert.NilError(t, err)
			defer resp.Body.Close()
			t.Logf("Event listener response status: %d", resp.StatusCode)

			verifyWebhookRejection(ctx, t, runcnx, targetNS, tt.rejectionReason)
			t.Logf("Pull request rejected as expected: %s", tt.rejectionReason)
		})
	}
}
