package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	ghinstallation "github.com/bradleyfalzon/ghinstallation/v2"
	oGitHub "github.com/google/go-github/v60/github"
	"github.com/google/go-github/v61/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	githubAppTokenMintBlockedLog         = "[SECURITY] Blocked GitHub App token minting before webhook signature validation completed"
	githubAppTokenExfiltrationBlockedLog = "[SECURITY][CRITICAL] Averted GitHub App credential exfiltration attempt before token minting"
	controllerWebhookSecretKey           = "webhook.secret"
)

// GetAppIDAndPrivateKey retrieves the GitHub application ID and private key from a secret in the specified namespace.
// It takes a context, namespace, and Kubernetes client as input parameters.
// It returns the application ID (int64), private key ([]byte), and an error if any.
func (v *Provider) GetAppIDAndPrivateKey(ctx context.Context, ns string, kube kubernetes.Interface) (int64, []byte, error) {
	paramsinfo := &v.Run.Info
	secret, err := kube.CoreV1().Secrets(ns).Get(ctx, paramsinfo.Controller.Secret, metav1.GetOptions{})
	if err != nil {
		return 0, []byte{}, fmt.Errorf("could not get the secret %s in ns %s: %w", paramsinfo.Controller.Secret, ns, err)
	}

	appID := secret.Data[keys.GithubApplicationID]
	applicationID, err := strconv.ParseInt(strings.TrimSpace(string(appID)), 10, 64)
	if err != nil {
		return 0, []byte{}, fmt.Errorf("could not parse the github application_id number from secret: %w", err)
	}

	privateKey := secret.Data[keys.GithubPrivateKey]
	return applicationID, privateKey, nil
}

func (v *Provider) GetAppToken(ctx context.Context, kube kubernetes.Interface, gheURL string, installationID int64, ns string) (string, error) {
	applicationID, privateKey, err := v.GetAppIDAndPrivateKey(ctx, ns, kube)
	if err != nil {
		return "", err
	}
	v.ApplicationID = &applicationID
	tr := http.DefaultTransport

	itr, err := ghinstallation.New(tr, applicationID, installationID, privateKey)
	if err != nil {
		return "", err
	}
	itr.InstallationTokenOptions = &oGitHub.InstallationTokenOptions{
		RepositoryIDs: v.RepositoryIDs,
	}

	// This is a hack when we have auth and api disassociated like in our
	// unittests since we are using a custom http server with httptest
	reqTokenURL := os.Getenv("PAC_GIT_PROVIDER_TOKEN_APIURL")
	if reqTokenURL != "" {
		itr.BaseURL = reqTokenURL
		v.APIURL = &reqTokenURL
		gheURL = strings.TrimSuffix(reqTokenURL, "/api/v3")
	}

	if gheURL != "" {
		if !strings.HasPrefix(gheURL, "https://") && !strings.HasPrefix(gheURL, "http://") {
			gheURL = "https://" + gheURL
		}
		uploadURL := gheURL + "/api/uploads"
		v.Client, _ = github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs(gheURL, uploadURL)
		itr.BaseURL = strings.TrimSuffix(v.Client.BaseURL.String(), "/")
	} else {
		v.Client = github.NewClient(&http.Client{Transport: itr})
	}

	// Get a token ASAP because we need it for setting private repos
	token, err := itr.Token(ctx)
	if err != nil {
		return "", err
	}
	v.Token = github.String(token)

	return token, err
}

func validateAppWebhookSignature(ctx context.Context, run *params.Run, event *info.Event) error {
	signature := event.Request.Header.Get(github.SHA256SignatureHeader)
	if signature == "" {
		signature = event.Request.Header.Get(github.SHA1SignatureHeader)
	}
	if signature == "" || signature == "sha1=" {
		return fmt.Errorf("no signature has been detected, for security reason we are not allowing webhooks that has no secret")
	}

	var err error
	event.Provider.WebhookSecret, err = getCurrentNSWebhookSecret(ctx, run)
	if err != nil {
		return err
	}
	if event.Provider.WebhookSecret == "" {
		return fmt.Errorf("no webhook secret has been set in controller secret")
	}
	return github.ValidateSignature(signature, event.Request.Payload, []byte(event.Provider.WebhookSecret))
}

func getCurrentNSWebhookSecret(ctx context.Context, run *params.Run) (string, error) {
	ns := info.GetNS(ctx)
	secret, err := run.Clients.Kube.CoreV1().Secrets(ns).Get(ctx, run.Info.Controller.Secret, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret.Data[controllerWebhookSecretKey])), nil
}

func (v *Provider) parseEventType(request *http.Request, event *info.Event) error {
	event.EventType = request.Header.Get("X-GitHub-Event")
	if event.EventType == "" {
		return fmt.Errorf("failed to find event type in request header")
	}

	event.Provider.URL = request.Header.Get("X-GitHub-Enterprise-Host")

	if event.EventType == "push" {
		event.TriggerTarget = triggertype.Push
	} else {
		event.TriggerTarget = triggertype.PullRequest
	}

	return nil
}

type Payload struct {
	Installation struct {
		ID *int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		HTMLURL string `json:"html_url"`
		ID      *int64 `json:"id"`
	} `json:"repository"`
}

func getInstallationAndRepoIDFromPayload(payload string) (int64, int64, error) {
	var data Payload
	err := json.Unmarshal([]byte(payload), &data)
	if err != nil {
		return -1, -1, err
	}

	var installationID int64 = -1
	var repoID int64 = -1
	if data.Installation.ID != nil {
		installationID = *data.Installation.ID
	}

	if data.Repository.ID != nil {
		repoID = *data.Repository.ID
	}

	return installationID, repoID, nil
}

func validateEnterpriseHostMatchesPayload(gheURL, payload string) error {
	if gheURL == "" {
		return nil
	}
	if !strings.HasPrefix(gheURL, "https://") && !strings.HasPrefix(gheURL, "http://") {
		gheURL = "https://" + gheURL
	}
	enterpriseURL, err := url.Parse(gheURL)
	if err != nil || enterpriseURL.Host == "" {
		return fmt.Errorf("invalid X-GitHub-Enterprise-Host header")
	}

	var data Payload
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return err
	}
	if data.Repository.HTMLURL == "" {
		return fmt.Errorf("repository HTML URL is missing in payload, cannot validate enterprise host")
	}
	repoURL, err := url.Parse(data.Repository.HTMLURL)
	if err != nil || repoURL.Host == "" {
		return fmt.Errorf("invalid repository URL in GitHub payload")
	}
	if !strings.EqualFold(enterpriseURL.Host, repoURL.Host) {
		return fmt.Errorf("github enterprise host %q does not match repository host %q", enterpriseURL.Host, repoURL.Host)
	}
	return nil
}

func (v *Provider) logBlockedGitHubAppTokenMint(request *http.Request, event *info.Event, installationID int64, reason string, err error) {
	if v.Logger == nil {
		return
	}
	v.Logger.Errorw(githubAppTokenExfiltrationBlockedLog,
		"severity", "critical",
		"security-impact", "github-app-jwt-exfiltration-blocked",
		"reason", reason,
		"error", err,
		"event-type", event.EventType,
		"installation-id", installationID,
		"github-enterprise-host-present", request.Header.Get("X-GitHub-Enterprise-Host") != "",
		"remote-addr", request.RemoteAddr,
	)
}

func (v *Provider) logGitHubAppTokenMintValidationFailure(request *http.Request, event *info.Event, installationID int64, reason string, err error) {
	if v.Logger == nil {
		return
	}
	v.Logger.Warnw(githubAppTokenMintBlockedLog,
		"severity", "warning",
		"security-impact", "github-app-token-mint-blocked",
		"reason", reason,
		"error", err,
		"event-type", event.EventType,
		"installation-id", installationID,
		"github-enterprise-host-present", request.Header.Get("X-GitHub-Enterprise-Host") != "",
		"remote-addr", request.RemoteAddr,
	)
}

// ParsePayload will parse the payload and return the event
// it generate the github app token targeting the installation id
// this pieces of code is a bit messy because we need first getting a token to
// before parsing the payload.
//
// We need to get the token at first because in some case when coming from pull request
// comment (or recheck from the UI) we will use that token to get
// information about the PR that is not part of the payload.
//
// We then regenerate a second time the token scoped to the repo where the
// payload come from so we can avoid the scenario where an admin install the
// app on a github org which has a mixed of private and public repos and some of
// the public users should not have access to the private repos.
//
// Validate the webhook signature before generating an app token because the token
// request signs a GitHub App JWT locally and sends it to the selected API host.
func (v *Provider) ParsePayload(ctx context.Context, run *params.Run, request *http.Request, payload string) (*info.Event, error) {
	// ParsePayload is really happening before SetClient so let's set this at first here.
	// Only apply for GitHub provider since we do fancy token creation at payload parsing
	v.Run = run
	event := info.NewEvent()
	event.Request = &info.Request{
		Header:  request.Header,
		Payload: bytes.TrimSpace([]byte(payload)),
	}
	systemNS := info.GetNS(ctx)
	if err := v.parseEventType(request, event); err != nil {
		return nil, err
	}

	installationIDFrompayload, repoIDFromPayload, err := getInstallationAndRepoIDFromPayload(payload)
	if err != nil {
		return nil, err
	}
	if installationIDFrompayload != -1 {
		var err error
		if err := validateAppWebhookSignature(ctx, run, event); err != nil {
			v.logGitHubAppTokenMintValidationFailure(request, event, installationIDFrompayload, "webhook-signature-validation-failed", err)
			return nil, err
		}
		if err := validateEnterpriseHostMatchesPayload(event.Provider.URL, payload); err != nil {
			v.logBlockedGitHubAppTokenMint(request, event, installationIDFrompayload, "enterprise-host-validation-failed", err)
			return nil, err
		}
		// TODO: move this out of here when we move al config inside context
		if event.Provider.Token, err = v.GetAppToken(ctx, run.Clients.Kube, event.Provider.URL, installationIDFrompayload, systemNS); err != nil {
			return nil, err
		}
	}

	if repoIDFromPayload > 0 {
		v.RepositoryIDs = []int64{repoIDFromPayload}
	}

	eventInt, err := github.ParseWebHook(event.EventType, []byte(payload))
	if err != nil {
		return nil, err
	}

	// should not get invalid json since we already check it in github.ParseWebHook
	_ = json.Unmarshal([]byte(payload), &eventInt)

	processedEvent, err := v.processEvent(ctx, event, eventInt)
	if err != nil {
		return nil, err
	}

	processedEvent.Event = eventInt
	processedEvent.InstallationID = installationIDFrompayload
	processedEvent.GHEURL = event.Provider.URL
	processedEvent.Provider.URL = event.Provider.URL

	// regenerate token scoped to the repo IDs
	if v.pacInfo.SecretGHAppRepoScoped && installationIDFrompayload != -1 && len(v.RepositoryIDs) > 0 {
		repoLists := []string{}
		if v.pacInfo.SecretGhAppTokenScopedExtraRepos != "" {
			// this is going to show up a lot in the logs but i guess that
			// would make people fix the value instead of being lost into
			// the top of the logs at controller start.
			for _, configValue := range strings.Split(v.pacInfo.SecretGhAppTokenScopedExtraRepos, ",") {
				configValueS := strings.TrimSpace(configValue)
				if configValueS == "" {
					continue
				}
				repoLists = append(repoLists, configValueS)
			}
			v.Logger.Infof("Github token scope extended to %v keeping SecretGHAppRepoScoped to true", repoLists)
		}
		token, err := v.CreateToken(ctx, repoLists, processedEvent)
		if err != nil {
			return nil, err
		}
		processedEvent.Provider.Token = token
	}

	return processedEvent, nil
}

func (v *Provider) processEvent(ctx context.Context, event *info.Event, eventInt interface{}) (*info.Event, error) {
	var processedEvent *info.Event
	var err error

	processedEvent = info.NewEvent()

	switch gitEvent := eventInt.(type) {
	case *github.CheckRunEvent:
		if v.Client == nil {
			return nil, fmt.Errorf("check run rerequest is only supported with github apps integration")
		}

		if *gitEvent.Action != "rerequested" {
			return nil, fmt.Errorf("only issue recheck is supported in checkrunevent")
		}
		return v.handleReRequestEvent(ctx, gitEvent)
	case *github.CheckSuiteEvent:
		if v.Client == nil {
			return nil, fmt.Errorf("check suite rerequest is only supported with github apps integration")
		}

		if *gitEvent.Action != "rerequested" {
			return nil, fmt.Errorf("only issue recheck is supported in checkrunevent")
		}
		return v.handleCheckSuites(ctx, gitEvent)
	case *github.IssueCommentEvent:
		if v.Client == nil {
			return nil, fmt.Errorf("gitops style comments operation is only supported with github apps integration")
		}
		if gitEvent.GetAction() != "created" {
			return nil, fmt.Errorf("only newly created comment is supported, received: %s", gitEvent.GetAction())
		}
		processedEvent, err = v.handleIssueCommentEvent(ctx, gitEvent)
		if err != nil {
			return nil, err
		}
	case *github.CommitCommentEvent:
		if v.Client == nil {
			return nil, fmt.Errorf("gitops style comments operation is only supported with github apps integration")
		}
		processedEvent, err = v.handleCommitCommentEvent(ctx, gitEvent)
		if err != nil {
			return nil, err
		}
	case *github.PushEvent:
		processedEvent.Organization = gitEvent.GetRepo().GetOwner().GetLogin()
		processedEvent.Repository = gitEvent.GetRepo().GetName()
		processedEvent.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		processedEvent.URL = gitEvent.GetRepo().GetHTMLURL()
		v.RepositoryIDs = []int64{gitEvent.GetRepo().GetID()}
		processedEvent.SHA = gitEvent.GetHeadCommit().GetID()
		// on push event we may not get a head commit but only
		if processedEvent.SHA == "" {
			processedEvent.SHA = gitEvent.GetBefore()
		}
		processedEvent.SHAURL = gitEvent.GetHeadCommit().GetURL()
		processedEvent.SHATitle = gitEvent.GetHeadCommit().GetMessage()
		processedEvent.Sender = gitEvent.GetSender().GetLogin()
		processedEvent.BaseBranch = gitEvent.GetRef()
		processedEvent.EventType = event.TriggerTarget.String()
		processedEvent.HeadBranch = processedEvent.BaseBranch // in push events Head Branch is the same as Basebranch
		processedEvent.BaseURL = gitEvent.GetRepo().GetHTMLURL()
		processedEvent.HeadURL = processedEvent.BaseURL // in push events Head URL is the same as BaseURL
	case *github.PullRequestEvent:
		processedEvent.Repository = gitEvent.GetRepo().GetName()
		processedEvent.Organization = gitEvent.GetRepo().Owner.GetLogin()
		processedEvent.DefaultBranch = gitEvent.GetRepo().GetDefaultBranch()
		processedEvent.SHA = gitEvent.GetPullRequest().Head.GetSHA()
		processedEvent.URL = gitEvent.GetRepo().GetHTMLURL()
		processedEvent.BaseBranch = gitEvent.GetPullRequest().Base.GetRef()
		processedEvent.HeadBranch = gitEvent.GetPullRequest().Head.GetRef()
		processedEvent.BaseURL = gitEvent.GetPullRequest().Base.GetRepo().GetHTMLURL()
		processedEvent.HeadURL = gitEvent.GetPullRequest().Head.GetRepo().GetHTMLURL()
		processedEvent.Sender = gitEvent.GetPullRequest().GetUser().GetLogin()
		processedEvent.EventType = event.EventType
		processedEvent.PullRequestNumber = gitEvent.GetPullRequest().GetNumber()
		processedEvent.PullRequestTitle = gitEvent.GetPullRequest().GetTitle()
		// getting the repository ids of the base and head of the pull request
		// to scope the token to
		v.RepositoryIDs = []int64{
			gitEvent.GetPullRequest().GetBase().GetRepo().GetID(),
		}
	default:
		return nil, errors.New("this event is not supported")
	}

	// check before overriding the value for TriggerTarget
	if processedEvent.TriggerTarget == "" {
		processedEvent.TriggerTarget = event.TriggerTarget
	}
	processedEvent.Provider.Token = event.Provider.Token

	return processedEvent, nil
}

func (v *Provider) handleReRequestEvent(ctx context.Context, event *github.CheckRunEvent) (*info.Event, error) {
	runevent := info.NewEvent()
	runevent.Organization = event.GetRepo().GetOwner().GetLogin()
	runevent.Repository = event.GetRepo().GetName()
	runevent.URL = event.GetRepo().GetHTMLURL()
	runevent.DefaultBranch = event.GetRepo().GetDefaultBranch()
	runevent.SHA = event.GetCheckRun().GetCheckSuite().GetHeadSHA()
	runevent.HeadBranch = event.GetCheckRun().GetCheckSuite().GetHeadBranch()
	runevent.HeadURL = event.GetCheckRun().GetCheckSuite().GetRepository().GetHTMLURL()
	// If we don't have a pull_request in this it probably mean a push
	if len(event.GetCheckRun().GetCheckSuite().PullRequests) == 0 {
		runevent.BaseBranch = runevent.HeadBranch
		runevent.BaseURL = runevent.HeadURL
		runevent.EventType = "push"
		// we allow the rerequest user here, not the push user, i guess it's
		// fine because you can't do a rereq without being a github owner?
		runevent.Sender = event.GetSender().GetLogin()
		v.RepositoryIDs = []int64{event.GetRepo().GetID()}
		return runevent, nil
	}
	runevent.PullRequestNumber = event.GetCheckRun().GetCheckSuite().PullRequests[0].GetNumber()
	runevent.TriggerTarget = triggertype.PullRequest
	v.Logger.Infof("Recheck of PR %s/%s#%d has been requested", runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
	return v.getPullRequest(ctx, runevent)
}

func (v *Provider) handleCheckSuites(ctx context.Context, event *github.CheckSuiteEvent) (*info.Event, error) {
	runevent := info.NewEvent()
	runevent.Organization = event.GetRepo().GetOwner().GetLogin()
	runevent.Repository = event.GetRepo().GetName()
	runevent.URL = event.GetRepo().GetHTMLURL()
	runevent.DefaultBranch = event.GetRepo().GetDefaultBranch()
	runevent.SHA = event.GetCheckSuite().GetHeadSHA()
	runevent.HeadBranch = event.GetCheckSuite().GetHeadBranch()
	runevent.HeadURL = event.GetCheckSuite().GetRepository().GetHTMLURL()
	// If we don't have a pull_request in this it probably mean a push
	// we are not able to know which
	if len(event.GetCheckSuite().PullRequests) == 0 {
		runevent.BaseBranch = runevent.HeadBranch
		runevent.BaseURL = runevent.HeadURL
		runevent.EventType = "push"
		runevent.TriggerTarget = "push"
		// we allow the rerequest user here, not the push user, i guess it's
		// fine because you can't do a rereq without being a github owner?
		runevent.Sender = event.GetSender().GetLogin()
		v.RepositoryIDs = []int64{event.GetRepo().GetID()}
		return runevent, nil
		// return nil, fmt.Errorf("check suite event is not supported for push events")
	}
	runevent.PullRequestNumber = event.GetCheckSuite().PullRequests[0].GetNumber()
	runevent.TriggerTarget = triggertype.PullRequest
	v.Logger.Infof("Rerun of all check on PR %s/%s#%d has been requested", runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
	return v.getPullRequest(ctx, runevent)
}

func convertPullRequestURLtoNumber(pullRequest string) (int, error) {
	prNumber, err := strconv.Atoi(path.Base(pullRequest))
	if err != nil {
		return -1, fmt.Errorf("bad pull request number html_url number: %w", err)
	}
	return prNumber, nil
}

func (v *Provider) handleIssueCommentEvent(ctx context.Context, event *github.IssueCommentEvent) (*info.Event, error) {
	action := "recheck"
	runevent := info.NewEvent()
	runevent.Organization = event.GetRepo().GetOwner().GetLogin()
	runevent.Repository = event.GetRepo().GetName()
	runevent.Sender = event.GetSender().GetLogin()
	// Always set the trigger target as pull_request on issue comment events
	runevent.TriggerTarget = triggertype.PullRequest
	if !event.GetIssue().IsPullRequest() {
		return info.NewEvent(), fmt.Errorf("issue comment is not coming from a pull_request")
	}
	opscomments.SetEventTypeAndTargetPR(runevent, event.GetComment().GetBody())
	// We are getting the full URL so we have to get the last part to get the PR number,
	// we don't have to care about URL query string/hash and other stuff because
	// that comes up from the API.
	var err error
	runevent.PullRequestNumber, err = convertPullRequestURLtoNumber(event.GetIssue().GetPullRequestLinks().GetHTMLURL())
	if err != nil {
		return info.NewEvent(), err
	}

	v.Logger.Infof("issue_comment: pipelinerun %s on %s/%s#%d has been requested", action, runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
	return v.getPullRequest(ctx, runevent)
}

func (v *Provider) handleCommitCommentEvent(ctx context.Context, event *github.CommitCommentEvent) (*info.Event, error) {
	action := "push"
	runevent := info.NewEvent()
	runevent.Organization = event.GetRepo().GetOwner().GetLogin()
	runevent.Repository = event.GetRepo().GetName()
	runevent.Sender = event.GetSender().GetLogin()
	runevent.URL = event.GetRepo().GetHTMLURL()
	runevent.SHA = event.GetComment().GetCommitID()
	runevent.HeadURL = runevent.URL
	runevent.BaseURL = runevent.HeadURL
	runevent.EventType = "push"
	runevent.TriggerTarget = "push"
	runevent.TriggerComment = event.GetComment().GetBody()
	v.RepositoryIDs = []int64{event.GetRepo().GetID()}

	// Set main as default branch to runevent.HeadBranch, runevent.BaseBranch
	runevent.HeadBranch, runevent.BaseBranch = "main", "main"
	var (
		branchName string
		prName     string
		err        error
	)

	// TODO: reuse the code from opscomments
	// If it is a /test or /retest comment with pipelinerun name figure out the pipelinerun name
	if provider.IsTestRetestComment(event.GetComment().GetBody()) {
		prName, branchName, err = provider.GetPipelineRunAndBranchNameFromTestComment(event.GetComment().GetBody())
		if err != nil {
			return runevent, err
		}
		runevent.TargetTestPipelineRun = prName
	}
	// Check for /cancel comment
	if provider.IsCancelComment(event.GetComment().GetBody()) {
		action = "cancellation"
		prName, branchName, err = provider.GetPipelineRunAndBranchNameFromCancelComment(event.GetComment().GetBody())
		if err != nil {
			return runevent, err
		}
		runevent.CancelPipelineRuns = true
		runevent.TargetCancelPipelineRun = prName
	}

	// If no branch is specified in GitOps comments, use runevent.HeadBranch
	if branchName == "" {
		branchName = runevent.HeadBranch
	}

	// Check if the specified branch contains the commit
	if err = v.isBranchContainsCommit(ctx, runevent, branchName); err != nil {
		if provider.IsCancelComment(event.GetComment().GetBody()) {
			runevent.CancelPipelineRuns = false
		}
		return runevent, err
	}
	// Finally update branch information to runevent.HeadBranch and runevent.BaseBranch
	runevent.HeadBranch = branchName
	runevent.BaseBranch = branchName

	v.Logger.Infof("commit_comment: pipelinerun %s on %s/%s#%s has been requested", action, runevent.Organization, runevent.Repository, runevent.SHA)
	return runevent, nil
}
