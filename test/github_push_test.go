//go:build e2e

package test

import (
	"context"
	"testing"

	tgithub "github.com/tektoncd/pipelines-as-code/test/pkg/github"
)

func TestGithubGHEWebhookPush(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github GHE push request on Webhook",
		YamlFiles: []string{"testdata/pipelinerun-on-push.yaml"},
		GHE:       true,
		Webhook:   true,
	}
	defer g.TearDown(ctx, t)
	g.RunPushRequest(ctx, t)
}

func TestGithubGHEPush(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github push request",
		YamlFiles: []string{"testdata/pipelinerun-on-push.yaml"},
		GHE:       true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPushRequestCELMatchOnTitle(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github push request test CEL match on title",
		YamlFiles: []string{"testdata/pipelinerun-cel-annotation-for-title-match.yaml"},
		GHE:       true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)
}
