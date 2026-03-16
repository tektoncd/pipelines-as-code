package gitea

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	pgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	tlogs "github.com/openshift-pipelines/pipelines-as-code/test/pkg/podlogs"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TestOpts struct {
	TargetRepoName        string
	StatusOnlyLatest      bool
	OnOrg                 bool
	NoPullRequestCreation bool
	SkipEventsCheck       bool
	TargetNS              string
	TargetEvent           string
	Settings              *v1alpha1.Settings
	Regexp                *regexp.Regexp
	YAMLFiles             map[string]string
	ExtraArgs             map[string]string
	RepoCRParams          *[]v1alpha1.Params
	GlobalRepoCRParams    *[]v1alpha1.Params
	CheckForStatus        string
	TargetRefName         string
	CheckForNumberStatus  int
	ConcurrencyLimit      *int
	ParamsRun             *params.Run
	GiteaCNX              pgitea.Provider
	Opts                  options.E2E
	PullRequest           *forgejo.PullRequest
	DefaultBranch         string
	GitCloneURL           string
	GitHTMLURL            string
	GiteaAPIURL           string
	GiteaPassword         string
	ExpectEvents          bool
	InternalGiteaURL      string
	Token                 string
	SHA                   string
	FileChanges           []scm.FileChange
	CreateSecret          []corev1.Secret
	ProviderType          string // defaults to "forgejo" if empty
}

func PostCommentOnPullRequest(t *testing.T, topt *TestOpts, body string) {
	_, _, err := topt.GiteaCNX.Client().CreateIssueComment(topt.Opts.Organization,
		topt.Opts.Repo, topt.PullRequest.Index,
		forgejo.CreateIssueCommentOption{Body: body})
	topt.ParamsRun.Clients.Log.Infof("Posted comment \"%s\" in %s", body, topt.PullRequest.HTMLURL)
	assert.NilError(t, err)
}

func checkEvents(t *testing.T, events *corev1.EventList, topts *TestOpts) {
	t.Helper()
	newEvents := make([]corev1.Event, 0)
	// filter out events that are not related to the test like checking for cancelled pipelineruns
	for i := len(events.Items) - 1; i >= 0; i-- {
		topts.ParamsRun.Clients.Log.Infof("Reason is %s", events.Items[i].Reason)
		if events.Items[i].Reason == "CancelInProgress" {
			continue
		}
		newEvents = append(newEvents, events.Items[i])
	}
	if len(newEvents) > 0 {
		topts.ParamsRun.Clients.Log.Infof("0 events expected in case of failure but got %d", len(newEvents))
		for _, em := range newEvents {
			topts.ParamsRun.Clients.Log.Infof("Event: Reason: %s Type: %s ReportingInstance: %s Message: %s", em.Reason, em.Type, em.ReportingInstance, em.Message)
		}
		t.FailNow()
	}
}

func AddLabelToIssue(t *testing.T, topt *TestOpts, label string) {
	var targetID int64
	allLabels, _, err := topt.GiteaCNX.Client().ListRepoLabels(topt.Opts.Organization, topt.Opts.Repo, forgejo.ListLabelsOptions{})
	assert.NilError(t, err)
	for _, l := range allLabels {
		if l.Name == label {
			targetID = l.ID
		}
	}

	opt := forgejo.IssueLabelsOption{Labels: []int64{targetID}}
	_, _, err = topt.GiteaCNX.Client().AddIssueLabels(topt.Opts.Organization, topt.Opts.Repo, topt.PullRequest.Index, opt)
	assert.NilError(t, err)
	topt.ParamsRun.Clients.Log.Infof("Added label \"%s\" to %s", label, topt.PullRequest.HTMLURL)
}

// TestPR will test the pull request event and grab comments from the PR.
func TestPR(t *testing.T, topts *TestOpts) (context.Context, func()) {
	ctx := context.Background()
	if topts.ParamsRun == nil {
		runcnx, opts, giteacnx, err := Setup(ctx)
		assert.NilError(t, err, fmt.Errorf("cannot do gitea setup: %w", err))
		topts.GiteaCNX = giteacnx
		topts.ParamsRun = runcnx
		topts.Opts = opts
	}
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	giteaURL := os.Getenv("TEST_GITEA_API_URL")
	giteaPassword := os.Getenv("TEST_GITEA_PASSWORD")
	topts.GiteaAPIURL = giteaURL
	topts.GiteaPassword = giteaPassword
	hookURL := os.Getenv("TEST_GITEA_SMEEURL")
	topts.InternalGiteaURL = os.Getenv("TEST_GITEA_INTERNAL_URL")
	if topts.InternalGiteaURL == "" {
		topts.InternalGiteaURL = "http://forgejo-http.forgejo:3000"
	}
	if topts.ExtraArgs == nil {
		topts.ExtraArgs = map[string]string{}
	}
	topts.ExtraArgs["ProviderURL"] = topts.InternalGiteaURL
	if topts.TargetNS == "" {
		topts.TargetNS = topts.TargetRefName
	}
	if topts.TargetRefName == "" {
		topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		topts.TargetNS = topts.TargetRefName
	}
	if err := pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun); err != nil {
		t.Logf("error creating namespace %s: %v", topts.TargetNS, err)
	}

	if topts.TargetRepoName == "" {
		topts.TargetRepoName = topts.TargetRefName
	}

	if topts.DefaultBranch == "" {
		topts.DefaultBranch = options.MainBranch
	}

	webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")
	repoInfo, err := CreateGiteaRepo(topts.GiteaCNX.Client(), topts.Opts.Organization,
		topts.TargetRepoName, topts.DefaultBranch, hookURL,
		webhookSecret, topts.OnOrg,
		topts.ParamsRun.Clients.Log)
	assert.NilError(t, err)
	topts.Opts.Repo = repoInfo.Name
	topts.Opts.Organization = repoInfo.Owner.UserName
	topts.DefaultBranch = repoInfo.DefaultBranch
	topts.GitHTMLURL = repoInfo.HTMLURL

	topts.Token, err = CreateToken(topts)
	assert.NilError(t, err)

	for _, sec := range topts.CreateSecret {
		ns := sec.GetNamespace()
		if ns == "" {
			ns = topts.TargetNS
		}
		_, err := topts.ParamsRun.Clients.Kube.CoreV1().Secrets(ns).Create(ctx, &sec, metav1.CreateOptions{})
		assert.NilError(t, err, "failed to create secret %s in namespace %s: %v", sec.GetName(), ns, err)
	}

	providerType := topts.ProviderType
	if providerType == "" {
		providerType = "forgejo"
	}
	gp := &v1alpha1.GitProvider{
		Type: providerType,
		// caveat this assume gitea running on the same cluster, which
		// we do and need for e2e tests but that may be changed somehow
		URL:    topts.InternalGiteaURL,
		Secret: &v1alpha1.Secret{Name: topts.TargetNS, Key: "token"},
	}
	spec := v1alpha1.RepositorySpec{
		URL:              topts.GitHTMLURL,
		ConcurrencyLimit: topts.ConcurrencyLimit,
		Params:           topts.RepoCRParams,
		Settings:         topts.Settings,
	}
	if topts.GlobalRepoCRParams == nil {
		spec.GitProvider = gp
	} else {
		spec.GitProvider = &v1alpha1.GitProvider{Type: providerType}
	}
	assert.NilError(t, CreateCRD(ctx, topts, spec, false))

	// we only test params for global repo settings for now we may change that if we want
	if topts.GlobalRepoCRParams != nil {
		spec := v1alpha1.RepositorySpec{
			Params:      topts.GlobalRepoCRParams,
			GitProvider: gp,
		}
		assert.NilError(t, CreateCRD(ctx, topts, spec, true))
	}

	cleanup := func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			defer TearDown(ctx, t, topts)
		}
	}

	url, err := scm.MakeGitCloneURL(repoInfo.CloneURL, os.Getenv("TEST_GITEA_USERNAME"), os.Getenv("TEST_GITEA_PASSWORD"))
	assert.NilError(t, err)
	topts.GitCloneURL = url

	if topts.NoPullRequestCreation {
		return ctx, cleanup
	}

	entries, err := payload.GetEntries(topts.YAMLFiles,
		topts.TargetNS,
		repoInfo.DefaultBranch,
		topts.TargetEvent,
		topts.ExtraArgs)
	assert.NilError(t, err)

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	topts.SHA = scm.PushFilesToRefGit(t, scmOpts, entries)

	topts.ParamsRun.Clients.Log.Infof("Creating PullRequest")
	for i := 0; i < 5; i++ {
		if topts.PullRequest, _, err = topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, repoInfo.Name, forgejo.CreatePullRequestOption{
			Title: "Test Pull Request - " + topts.TargetRefName,
			Head:  topts.TargetRefName,
			Base:  topts.DefaultBranch,
		}); err == nil {
			break
		}
		topts.ParamsRun.Clients.Log.Infof("Creating PullRequest has failed, retrying %d/%d, err", i, 5, err)
		if i == 4 {
			t.Fatalf("cannot create pull request: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", topts.PullRequest.HTMLURL)

	if topts.CheckForStatus != "" {
		WaitForStatus(t, topts, topts.TargetRefName, "", topts.StatusOnlyLatest)
	}

	if topts.Regexp != nil {
		WaitForPullRequestCommentMatch(t, topts)
	}

	events, err := topts.ParamsRun.Clients.Kube.CoreV1().Events(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.Repository, formatting.CleanValueKubernetes(topts.TargetNS)),
	})
	assert.NilError(t, err)
	if topts.ExpectEvents {
		// in some cases event is expected but it takes time
		// to emit and before that this check gets executed
		// so adds a sleep for that case eg. TestGiteaBadYaml
		if len(events.Items) == 0 {
			// loop 30 times over a 5 second period and try to get any events
			for i := 0; i < 30; i++ {
				events, err = topts.ParamsRun.Clients.Kube.CoreV1().Events(topts.TargetNS).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("%s=%s", keys.Repository, formatting.CleanValueKubernetes(topts.TargetNS)),
				})
				assert.NilError(t, err)
				if len(events.Items) > 0 {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}
		assert.Assert(t, len(events.Items) != 0, "events expected in case of failure but got 0")
	} else if !topts.SkipEventsCheck {
		checkEvents(t, events, topts)
	}
	return ctx, cleanup
}

func NewPR(t *testing.T, topts *TestOpts) func() {
	ctx := context.Background()
	if topts.ParamsRun == nil {
		runcnx, opts, giteacnx, err := Setup(ctx)
		assert.NilError(t, err, fmt.Errorf("cannot do gitea setup: %w", err))
		topts.GiteaCNX = giteacnx
		topts.ParamsRun = runcnx
		topts.Opts = opts
	}
	giteaURL := os.Getenv("TEST_GITEA_API_URL")
	giteaPassword := os.Getenv("TEST_GITEA_PASSWORD")
	topts.GiteaAPIURL = giteaURL
	topts.GiteaPassword = giteaPassword
	topts.InternalGiteaURL = os.Getenv("TEST_GITEA_INTERNAL_URL")
	if topts.InternalGiteaURL == "" {
		topts.InternalGiteaURL = "http://forgejo-http.forgejo:3000"
	}
	if topts.ExtraArgs == nil {
		topts.ExtraArgs = map[string]string{}
	}
	topts.ExtraArgs["ProviderURL"] = topts.InternalGiteaURL
	if topts.TargetNS == "" {
		topts.TargetNS = topts.TargetRefName
	}
	if topts.TargetRefName == "" {
		topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		topts.TargetNS = topts.TargetRefName
		assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	}
	if topts.TargetRepoName == "" {
		topts.TargetRepoName = topts.TargetRefName
	}

	repoInfo, err := GetGiteaRepo(topts.GiteaCNX.Client(), topts.Opts.Organization, topts.TargetRepoName, topts.ParamsRun.Clients.Log)
	assert.NilError(t, err)
	topts.Opts.Repo = repoInfo.Name
	topts.Opts.Organization = repoInfo.Owner.UserName
	topts.DefaultBranch = repoInfo.DefaultBranch
	topts.GitHTMLURL = repoInfo.HTMLURL

	cleanup := func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			defer TearDown(ctx, t, topts)
		}
	}
	// topts.Token, err = CreateToken(topts)
	// assert.NilError(t, err)

	// assert.NilError(t, CreateCRD(ctx, topts))

	url, err := scm.MakeGitCloneURL(repoInfo.CloneURL, os.Getenv("TEST_GITEA_USERNAME"), os.Getenv("TEST_GITEA_PASSWORD"))
	assert.NilError(t, err)
	topts.GitCloneURL = url

	if topts.NoPullRequestCreation {
		return cleanup
	}

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	scm.ChangeFilesRefGit(t, scmOpts, topts.FileChanges)

	topts.ParamsRun.Clients.Log.Infof("Creating PullRequest")
	for i := 0; i < 5; i++ {
		if topts.PullRequest, _, err = topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, repoInfo.Name, forgejo.CreatePullRequestOption{
			Title: "Test Pull Request - " + topts.TargetRefName,
			Head:  topts.TargetRefName,
			Base:  options.MainBranch,
		}); err == nil {
			break
		}
		topts.ParamsRun.Clients.Log.Infof("Creating PullRequest has failed, retrying %d/%d, err", i, 5, err)
		if i == 4 {
			t.Fatalf("cannot create pull request: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", topts.PullRequest.HTMLURL)

	if topts.CheckForStatus != "" {
		WaitForStatus(t, topts, topts.TargetRefName, "", topts.StatusOnlyLatest)
	}

	if topts.Regexp != nil {
		WaitForPullRequestCommentMatch(t, topts)
	}

	events, err := topts.ParamsRun.Clients.Kube.CoreV1().Events(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.Repository, formatting.CleanValueKubernetes(topts.TargetNS)),
	})
	assert.NilError(t, err)
	if topts.ExpectEvents {
		// in some cases event is expected but it takes time
		// to emit and before that this check gets executed
		// so adds a sleep for that case eg. TestGiteaBadYaml
		if len(events.Items) == 0 {
			// loop 30 times over a 5 second period and try to get any events
			for i := 0; i < 30; i++ {
				events, err = topts.ParamsRun.Clients.Kube.CoreV1().Events(topts.TargetNS).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("%s=%s", keys.Repository, formatting.CleanValueKubernetes(topts.TargetNS)),
				})
				assert.NilError(t, err)
				if len(events.Items) > 0 {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}
		assert.Assert(t, len(events.Items) != 0, "events expected in case of failure but got 0")
	} else if !topts.SkipEventsCheck {
		checkEvents(t, events, topts)
	}
	return cleanup
}

func WaitForStatus(t *testing.T, topts *TestOpts, ref, forcontext string, onlylatest bool) {
	i := 0
	if strings.HasPrefix(ref, "heads/") {
		refo, _, err := topts.GiteaCNX.Client().GetRepoRefs(topts.Opts.Organization, topts.Opts.Repo, ref)
		assert.NilError(t, err)
		ref = refo[0].Object.SHA
	}
	checkNumberOfStatus := topts.CheckForNumberStatus
	if checkNumberOfStatus == 0 {
		checkNumberOfStatus = 1
	}
	for {
		numstatus := 0
		// get first sha of tree ref
		statuses, _, err := topts.GiteaCNX.Client().ListStatuses(topts.Opts.Organization, topts.Opts.Repo, ref, forgejo.ListStatusesOption{})
		assert.NilError(t, err)
		// sort statuses by id
		sort.Slice(statuses, func(i, j int) bool {
			return statuses[i].ID < statuses[j].ID
		})
		if onlylatest {
			if len(statuses) > 1 {
				statuses = statuses[len(statuses)-1:]
			} else {
				time.Sleep(5 * time.Second)
				continue
			}
		}
		for _, cstatus := range statuses {
			if topts.CheckForStatus == "Skipped" {
				if strings.HasSuffix(cstatus.Description, "Pending approval, waiting for an /ok-to-test") {
					numstatus++
					break
				}
			}
			if cstatus.State == "pending" {
				continue
			}
			if forcontext != "" && cstatus.Context != forcontext {
				continue
			}
			statuscheck := topts.CheckForStatus
			if statuscheck != "" && statuscheck != string(cstatus.State) {
				if statuscheck != cstatus.Description {
					t.Fatalf("Status on SHA: %s is %s from %s", ref, cstatus.State, cstatus.Context)
				}
			}
			topts.ParamsRun.Clients.Log.Infof("Status on SHA: %s is %s from %s", ref, cstatus.State, cstatus.Context)
			numstatus++
		}
		topts.ParamsRun.Clients.Log.Infof("Number of gitea status on PR: %d/%d", numstatus, checkNumberOfStatus)
		if numstatus == checkNumberOfStatus {
			return
		}
		if numstatus > checkNumberOfStatus {
			t.Fatalf("Number of statuses is greater than expected, statuses: %d, expected: %d", numstatus, checkNumberOfStatus)
		}
		if i > 50 {
			t.Fatalf("gitea status has not been updated")
		}
		time.Sleep(5 * time.Second)
		i++
	}
}

func WaitForSecretDeletion(t *testing.T, topts *TestOpts, _ string) {
	i := 0
	for {
		// make sure pipelineRuns are deleted, before checking secrets
		list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).
			List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app.kubernetes.io/managed-by=%v", pipelinesascode.GroupName),
			})
		assert.NilError(t, err)

		if i > 5 {
			t.Fatalf("pipelineruns are not removed from the target namespace, something is fishy")
		}
		if len(list.Items) == 0 {
			break
		}
		topts.ParamsRun.Clients.Log.Infof("deleting pipelineRuns in %v namespace", topts.TargetNS)
		err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).
			DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app.kubernetes.io/managed-by=%v", pipelinesascode.GroupName),
			})
		assert.NilError(t, err)

		time.Sleep(5 * time.Second)
		i++
	}

	topts.ParamsRun.Clients.Log.Infof("checking secrets in %v namespace", topts.TargetNS)
	i = 0
	for {
		list, err := topts.ParamsRun.Clients.Kube.CoreV1().Secrets(topts.TargetNS).
			List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s\n", keys.URLRepository, topts.TargetNS),
			})
		assert.NilError(t, err)

		if len(list.Items) == 0 {
			break
		}

		if i > 5 {
			t.Fatalf("secret has not removed from the target namespace, something is fishy")
		}
		time.Sleep(5 * time.Second)
		i++
	}
}

func WaitForPullRequestCommentMatch(t *testing.T, topts *TestOpts) {
	i := 0
	topts.ParamsRun.Clients.Log.Infof("Looking for regexp \"%s\" in PR comments", topts.Regexp.String())
	for {
		comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
		assert.NilError(t, err)
		for _, v := range comments {
			if topts.Regexp.MatchString(v.Body) {
				topts.ParamsRun.Clients.Log.Infof("Found regexp in comment: %s", v.Body)
				return
			}
		}
		if i > 60 {
			t.Fatalf("gitea driver has not been posted any comment")
		}
		time.Sleep(2 * time.Second)
		i++
	}
}

// WaitForPullRequestCommentGoldenMatch will wait for a comment matching exactly (golden style) the content.
func WaitForPullRequestCommentGoldenMatch(t *testing.T, topts *TestOpts, goldenFile string) {
	i := 0
	if topts.Regexp == nil {
		t.Fatalf("topts.Regexp cannot be nil")
	}

	for {
		comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
		assert.NilError(t, err)
		for _, v := range comments {
			if v.Body == "" {
				continue
			}
			// we do first match on regexp in a comment and then golden string, or golden string update will get the first comment.
			if !topts.Regexp.MatchString(v.Body) {
				continue
			}
			topts.ParamsRun.Clients.Log.Infof("Found regexp match in comment: %s", topts.Regexp.String())
			if golden.String(v.Body, goldenFile)().Success() {
				topts.ParamsRun.Clients.Log.Infof("Found golden match in comment: %s", v.Body)
				return
			}
		}
		if i > 60 {
			t.Fatalf("we did not match the expected golden file output in the gitea comments after 2 minutes")
		}
		time.Sleep(2 * time.Second)
		i++
	}
}

func CheckIfPipelineRunsCancelled(t *testing.T, topts *TestOpts) {
	i := 0
	for {
		list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).
			List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%v=%v", keys.Repository, formatting.CleanValueKubernetes((topts.TargetNS))),
			})
		assert.NilError(t, err)

		if len(list.Items) == 0 {
			t.Fatalf("pipelineruns not found, where are they???")
		}

		if list.Items[0].Spec.Status == v1.PipelineRunSpecStatusCancelledRunFinally {
			topts.ParamsRun.Clients.Log.Info("PipelineRun is cancelled, yay!")
			break
		}

		if i > 5 {
			t.Fatalf("pipelineruns are not cancelled, something is fishy")
		}
		time.Sleep(5 * time.Second)
		i++
	}
}

func GetStandardParams(t *testing.T, topts *TestOpts, eventType string) (repoURL, sourceURL, sourceBranch, targetBranch string) {
	t.Helper()
	var err error
	prs := &v1.PipelineRunList{}
	for i := 0; i < 21; i++ {
		prs, err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{
			LabelSelector: keys.EventType + "=" + eventType,
		})
		assert.NilError(t, err)
		// get all pipelinerun names
		names := make([]string, len(prs.Items))
		for i, pr := range prs.Items {
			names[i] = pr.Name
		}
		assert.Equal(t, len(prs.Items), 1, "should have only one "+eventType+" pipelinerun", names)

		if prs.Items[0].Status.Status.Conditions[0].Reason == "Succeeded" || prs.Items[0].Status.Status.Conditions[0].Reason == "Failed" {
			break
		}
		time.Sleep(5 * time.Second)
		if i == 20 {
			t.Fatalf("pipelinerun has not finished, something is fishy")
		}
	}
	numLines := int64(10)
	out, err := tlogs.GetPodLog(context.Background(),
		topts.ParamsRun.Clients.Kube.CoreV1(),
		topts.TargetNS, fmt.Sprintf("tekton.dev/pipelineRun=%s",
			prs.Items[0].Name), "step-test-standard-params-value",
		&numLines, nil)
	assert.NilError(t, err)
	assert.Assert(t, out != "")
	out = strings.TrimSpace(out)
	outputDataForPR := strings.Split(out, "--")
	if len(outputDataForPR) != 5 {
		t.Fatalf("expected 5 values in outputDataForPR, got %d: %v", len(outputDataForPR), outputDataForPR)
	}

	repoURL = outputDataForPR[0]
	sourceURL = strings.TrimPrefix(outputDataForPR[1], "\n")
	sourceBranch = strings.TrimPrefix(outputDataForPR[2], "\n")
	targetBranch = strings.TrimPrefix(outputDataForPR[3], "\n")

	return repoURL, sourceURL, sourceBranch, targetBranch
}

func VerifyConcurrency(t *testing.T, topts *TestOpts, globalRepoConcurrencyLimit *int) {
	t.Helper()
	ctx := context.Background()
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = topts.TargetRefName
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	globalNs, _, err := params.GetInstallLocation(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	ctx = info.StoreNS(ctx, globalNs)

	err = CreateCRD(ctx, topts,
		v1alpha1.RepositorySpec{
			ConcurrencyLimit: globalRepoConcurrencyLimit,
		},
		true)
	assert.NilError(t, err)

	defer (func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			topts.ParamsRun.Clients.Log.Infof("Cleaning up global repo %s in %s", info.DefaultGlobalRepoName, globalNs)
			err = topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(globalNs).Delete(
				context.Background(), info.DefaultGlobalRepoName, metav1.DeleteOptions{})
			assert.NilError(t, err)
		}
	})()

	_, f := TestPR(t, topts)
	defer f()
}
