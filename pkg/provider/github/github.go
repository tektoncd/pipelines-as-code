package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"k8s.io/client-go/kubernetes"
)

const (
	apiPublicURL = "https://api.github.com/"
	// TODO: makes this configurable for GHE in the ConfigMap.
	// on our GHE instance, it looks like this :
	// https://raw.ghe.openshiftpipelines.com/pac/chmouel-test/main/README.md
	// we can perhaps do some autodetection with event.Provider.GHEURL and adding
	// a raw into it.
	publicRawURLHost = "raw.githubusercontent.com"

	defaultPaginedNumber = 100
)

var _ provider.Interface = (*Provider)(nil)

type Provider struct {
	ghClient      *github.Client
	Logger        *zap.SugaredLogger
	Run           *params.Run
	pacInfo       *info.PacOpts
	Token, APIURL *string
	ApplicationID *int64
	providerName  string
	provenance    string
	RepositoryIDs []int64
	repo          *v1alpha1.Repository
	eventEmitter  *events.EventEmitter
	PaginedNumber int
	userType      string // The type of user i.e bot or not
	skippedRun
	triggerEvent string
}

type skippedRun struct {
	mutex      *sync.Mutex
	checkRunID int64
}

func New() *Provider {
	return &Provider{
		APIURL:        github.Ptr(keys.PublicGithubAPIURL),
		PaginedNumber: defaultPaginedNumber,
		skippedRun: skippedRun{
			mutex: &sync.Mutex{},
		},
	}
}

func (v *Provider) Client() *github.Client {
	return v.ghClient
}

func (v *Provider) SetGithubClient(client *github.Client) {
	v.ghClient = client
}

func (v *Provider) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
}

// detectGHERawURL Detect if we have a raw URL in GHE.
func detectGHERawURL(event *info.Event, taskHost string) bool {
	gheURL, err := url.Parse(event.GHEURL)
	if err != nil {
		// should not happen but may as well make sure
		return false
	}
	return taskHost == fmt.Sprintf("raw.%s", gheURL.Host)
}

// splitGithubURL Take a Github url and split it with org/repo path ref, supports rawURL.
func splitGithubURL(event *info.Event, uri string) (string, string, string, string, error) {
	pURL, err := url.Parse(uri)
	if err != nil {
		return "", "", "", "", fmt.Errorf("URL %s is not a valid provider URL: %w", uri, err)
	}
	path := pURL.Path
	if pURL.RawPath != "" {
		path = pURL.RawPath
	}
	split := strings.Split(path, "/")
	if len(split) <= 3 {
		return "", "", "", "", fmt.Errorf("URL %s does not seem to be a proper provider url: %w", uri, err)
	}
	var spOrg, spRepo, spRef, spPath string
	switch {
	case (pURL.Host == publicRawURLHost || detectGHERawURL(event, pURL.Host)) && len(split) >= 5:
		spOrg = split[1]
		spRepo = split[2]
		spRef = split[3]
		spPath = strings.Join(split[4:], "/")
	case split[3] == "blob" && len(split) >= 5:
		spOrg = split[1]
		spRepo = split[2]
		spRef = split[4]
		spPath = strings.Join(split[5:], "/")
	default:
		return "", "", "", "", fmt.Errorf("cannot recognize task as a GitHub URL to fetch: %s", uri)
	}
	// url decode the org, repo, ref and path
	if spRef, err = url.QueryUnescape(spRef); err != nil {
		return "", "", "", "", fmt.Errorf("cannot decode ref: %w", err)
	}
	if spPath, err = url.QueryUnescape(spPath); err != nil {
		return "", "", "", "", fmt.Errorf("cannot decode path: %w", err)
	}
	if spOrg, err = url.QueryUnescape(spOrg); err != nil {
		return "", "", "", "", fmt.Errorf("cannot decode org: %w", err)
	}
	if spRepo, err = url.QueryUnescape(spRepo); err != nil {
		return "", "", "", "", fmt.Errorf("cannot decode repo: %w", err)
	}
	return spOrg, spRepo, spPath, spRef, nil
}

func (v *Provider) GetTaskURI(ctx context.Context, event *info.Event, uri string) (bool, string, error) {
	if ret := provider.CompareHostOfURLS(uri, event.URL); !ret {
		return false, "", nil
	}

	spOrg, spRepo, spPath, spRef, err := splitGithubURL(event, uri)
	if err != nil {
		return false, "", err
	}
	nEvent := info.NewEvent()
	nEvent.Organization = spOrg
	nEvent.Repository = spRepo
	nEvent.BaseBranch = spRef
	ret, err := v.GetFileInsideRepo(ctx, nEvent, spPath, spRef)
	if err != nil {
		return false, "", err
	}
	return true, ret, nil
}

func (v *Provider) InitAppClient(ctx context.Context, kube kubernetes.Interface, event *info.Event) error {
	var err error
	// TODO: move this out of here when we move al config inside context
	ns := info.GetNS(ctx)
	event.Provider.Token, err = v.GetAppToken(ctx, kube, event.GHEURL, event.InstallationID, ns)
	if err != nil {
		return err
	}

	return nil
}

func (v *Provider) SetLogger(logger *zap.SugaredLogger) {
	v.Logger = logger
}

func (v *Provider) Validate(_ context.Context, _ *params.Run, event *info.Event) error {
	signature := event.Request.Header.Get(github.SHA256SignatureHeader)

	if signature == "" {
		signature = event.Request.Header.Get(github.SHA1SignatureHeader)
	}
	if signature == "" || signature == "sha1=" {
		// if no signature is present then don't validate, because user hasn't set one
		return fmt.Errorf("no signature has been detected, for security reason we are not allowing webhooks that has no secret")
	}
	if event.Provider.WebhookSecret == "" {
		return fmt.Errorf("no webhook secret has been set, in repository CR or secret")
	}
	return github.ValidateSignature(signature, event.Request.Payload, []byte(event.Provider.WebhookSecret))
}

func (v *Provider) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{
		TaskStatusTMPL: taskStatusTemplate,
		APIURL:         apiPublicURL,
		Name:           v.providerName,
	}
}

func MakeClient(ctx context.Context, apiURL, token string) (*github.Client, string, *string) {
	var client *github.Client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)

	tc := oauth2.NewClient(ctx, ts)
	if apiURL != "" {
		if !strings.HasPrefix(apiURL, "https") && !strings.HasPrefix(apiURL, "http") {
			apiURL = "https://" + apiURL
		}
	}

	providerName := "github"
	if apiURL != "" && apiURL != apiPublicURL {
		providerName = "github-enterprise"
		uploadURL := apiURL + "/api/uploads"
		client, _ = github.NewClient(tc).WithEnterpriseURLs(apiURL, uploadURL)
	} else {
		client = github.NewClient(tc)
		apiURL = client.BaseURL.String()
	}

	return client, providerName, github.Ptr(apiURL)
}

func parseTS(headerTS string) (time.Time, error) {
	ts := time.Time{}
	// Normal UTC: 2023-01-31 23:00:00 UTC
	if t, err := time.Parse("2006-01-02 15:04:05 MST", headerTS); err == nil {
		ts = t
	}

	// With TZ(???), ie: a token from Christoph 2023-04-26 23:23:26 +2000
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", headerTS); err == nil {
		ts = t
	}
	if ts.Year() == 1 {
		return ts, fmt.Errorf("cannot parse token expiration date: %s", headerTS)
	}

	return ts, nil
}

// checkWebhookSecretValidity check the webhook secret is valid and not
// ratelimited. we try to check first the header is set (unlimited life token  would
// not have an expiration) we would anyway get a 401 error when trying to use it
// but this gives a nice hint to the user into their namespace event of where
// the issue was.
func (v *Provider) checkWebhookSecretValidity(ctx context.Context, cw clockwork.Clock) error {
	rl, resp, err := wrapAPI(v, "check_rate_limit", func() (*github.RateLimits, *github.Response, error) {
		return v.Client().RateLimit.Get(ctx)
	})
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		v.Logger.Info("skipping checking if token has expired, rate_limit api is not enabled on token")
		return nil
	}

	if err != nil {
		return fmt.Errorf("error making request to the GitHub API checking rate limit: %w", err)
	}

	if resp != nil && resp.Header.Get("GitHub-Authentication-Token-Expiration") != "" {
		ts, err := parseTS(resp.Header.Get("GitHub-Authentication-Token-Expiration"))
		if err != nil {
			return fmt.Errorf("error parsing token expiration date: %w", err)
		}

		if cw.Now().After(ts) {
			errMsg := fmt.Sprintf("token has expired at %s", resp.TokenExpiration.Format(time.RFC1123))
			return fmt.Errorf("%s", errMsg)
		}
	}

	// Guard against nil rl or rl.SCIM which could lead to a panic.
	if rl == nil || rl.SCIM == nil {
		v.Logger.Info("skipping token expiration check, SCIM rate limit API is not available for this token")
		return nil
	}

	if rl.SCIM.Remaining == 0 {
		return fmt.Errorf("api rate limit exceeded. Access will be restored at %s", rl.SCIM.Reset.Format(time.RFC1123))
	}
	return nil
}

func (v *Provider) SetClient(ctx context.Context, run *params.Run, event *info.Event, repo *v1alpha1.Repository, eventsEmitter *events.EventEmitter) error {
	client, providerName, apiURL := MakeClient(ctx, event.Provider.URL, event.Provider.Token)
	v.providerName = providerName
	v.Run = run
	v.repo = repo
	v.eventEmitter = eventsEmitter
	v.triggerEvent = event.EventType

	// check that the Client is not already set, so we don't override our fakeclient
	// from unittesting.
	if v.ghClient == nil {
		v.ghClient = client
	}
	if v.ghClient == nil {
		return fmt.Errorf("no github client has been initialized")
	}

	// Added log for security audit purposes to log client access when a token is used
	integration := "github-webhook"
	if event.InstallationID != 0 {
		integration = "github-app"
	}
	run.Clients.Log.Infof(integration+": initialized OAuth2 client for providerName=%s providerURL=%s", v.providerName, event.Provider.URL)

	v.APIURL = apiURL

	if event.Provider.WebhookSecretFromRepo {
		// check the webhook secret is valid and not ratelimited
		if err := v.checkWebhookSecretValidity(ctx, clockwork.NewRealClock()); err != nil {
			return fmt.Errorf("the webhook secret is not valid: %w", err)
		}
	}

	// Handle GitHub App token scoping for both global and repo-level configuration
	if event.InstallationID > 0 {
		token := ""
		if repo != nil && v.pacInfo != nil && v.Logger != nil && v.eventEmitter != nil {
			v.Logger.Debugf("setupAuthenticatedClient: scoping github app token")
			scopedToken, err := ScopeTokenToListOfRepos(ctx, v, v.pacInfo, repo, run, event, v.eventEmitter, v.Logger)
			if err != nil {
				return fmt.Errorf("failed to scope token: %w", err)
			}
			token = scopedToken
		}
		// If Global and Repo level configurations are not provided then lets not override the provider token.
		if token != "" {
			event.Provider.Token = token
		} else if len(v.RepositoryIDs) > 0 {
			// We need to keep the token unscoped until ScopeTokenToListOfRepos so that CreateToken can
			// look up the extra repos from the configmap.
			// Token is scoped to only the calling repo if no additional scoping repos are configured
			// so that no unwanted remote tasks are executed.
			ns := info.GetNS(ctx)
			scopedToken, err := v.GetAppToken(ctx, run.Clients.Kube, event.Provider.URL, event.InstallationID, ns)
			if err != nil {
				return fmt.Errorf("failed to scope token to triggering repository: %w", err)
			}
			event.Provider.Token = scopedToken
		}
	}

	return nil
}

// GetTektonDir Get all yaml files in tekton directory return as a single concated file.
func (v *Provider) GetTektonDir(ctx context.Context, runevent *info.Event, path, provenance string) (string, error) {
	tektonDirSha := ""

	v.provenance = provenance
	// default set provenance from the SHA
	revision := runevent.SHA
	if provenance == "default_branch" {
		revision = runevent.DefaultBranch
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", runevent.DefaultBranch)
	} else {
		prInfo := ""
		if runevent.TriggerTarget == triggertype.PullRequest {
			prInfo = fmt.Sprintf("%s/%s#%d", runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
		}
		v.Logger.Infof("Using PipelineRun definition from source %s %s on commit SHA %s", runevent.TriggerTarget.String(), prInfo, runevent.SHA)
	}

	rootobjects, _, err := wrapAPI(v, "get_root_tree", func() (*github.Tree, *github.Response, error) {
		return v.Client().Git.GetTree(ctx, runevent.Organization, runevent.Repository, revision, false)
	})
	if err != nil {
		return "", err
	}
	for _, object := range rootobjects.Entries {
		if object.GetPath() == path {
			if object.GetType() != "tree" {
				return "", fmt.Errorf("%s has been found but is not a directory", path)
			}
			tektonDirSha = object.GetSHA()
		}
	}

	// If we didn't find a .tekton directory then just silently ignore the error.
	if tektonDirSha == "" {
		return "", nil
	}

	// Get all files in the .tekton directory recursively
	// there is a limit on this recursive calls to 500 entries, as documented here:
	// https://docs.github.com/en/rest/reference/git#get-a-tree
	// so we may need to address it in the future.
	tektonDirObjects, _, err := wrapAPI(v, "get_tekton_tree", func() (*github.Tree, *github.Response, error) {
		return v.Client().Git.GetTree(ctx, runevent.Organization, runevent.Repository, tektonDirSha,
			true)
	})
	if err != nil {
		return "", err
	}
	return v.concatAllYamlFiles(ctx, tektonDirObjects.Entries, runevent)
}

// GetCommitInfo get info (url and title) on a commit in runevent, this needs to
// be run after sewebhook while we already matched a token.
func (v *Provider) GetCommitInfo(ctx context.Context, runevent *info.Event) error {
	if v.ghClient == nil {
		return fmt.Errorf("no github client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}

	// if we don't have a sha we may have a branch (ie: incoming webhook) then
	// use the branch as sha since github supports it
	var commit *github.Commit
	sha := runevent.SHA
	if runevent.SHA == "" && runevent.HeadBranch != "" {
		branchinfo, _, err := wrapAPI(v, "get_branch_info", func() (*github.Branch, *github.Response, error) {
			return v.Client().Repositories.GetBranch(ctx, runevent.Organization, runevent.Repository, runevent.HeadBranch, 1)
		})
		if err != nil {
			return err
		}
		sha = branchinfo.Commit.GetSHA()
	}
	var err error
	commit, _, err = wrapAPI(v, "get_commit", func() (*github.Commit, *github.Response, error) {
		return v.Client().Git.GetCommit(ctx, runevent.Organization, runevent.Repository, sha)
	})
	if err != nil {
		return err
	}

	runevent.SHAURL = commit.GetHTMLURL()
	runevent.SHATitle = strings.Split(commit.GetMessage(), "\n\n")[0]
	runevent.SHA = commit.GetSHA()

	// Populate full commit information for LLM context
	runevent.SHAMessage = commit.GetMessage()
	if commit.Author != nil {
		runevent.SHAAuthorName = commit.Author.GetName()
		runevent.SHAAuthorEmail = commit.Author.GetEmail()
		if commit.Author.Date != nil {
			runevent.SHAAuthorDate = commit.Author.Date.Time
		}
	}
	if commit.Committer != nil {
		runevent.SHACommitterName = commit.Committer.GetName()
		runevent.SHACommitterEmail = commit.Committer.GetEmail()
		if commit.Committer.Date != nil {
			runevent.SHACommitterDate = commit.Committer.Date.Time
		}
	}

	return nil
}

// GetFileInsideRepo Get a file via Github API using the runinfo information, we
// branch is true, the user the branch as ref instead of the SHA
// TODO: merge GetFileInsideRepo amd GetTektonDir.
func (v *Provider) GetFileInsideRepo(ctx context.Context, runevent *info.Event, path, target string) (string, error) {
	ref := runevent.SHA
	if target != "" {
		ref = runevent.BaseBranch
	} else if v.provenance == "default_branch" {
		ref = runevent.DefaultBranch
	}

	fp, objects, _, err := wrapAPIGetContents(v, "get_file_contents", func() (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
		return v.Client().Repositories.GetContents(ctx, runevent.Organization,
			runevent.Repository, path, &github.RepositoryContentGetOptions{Ref: ref})
	})
	if err != nil {
		return "", err
	}
	if objects != nil {
		return "", fmt.Errorf("referenced file inside the Github Repository %s is a directory", path)
	}

	getobj, err := v.getObject(ctx, fp.GetSHA(), runevent)
	if err != nil {
		return "", err
	}

	return string(getobj), nil
}

// concatAllYamlFiles concat all yaml files from a directory as one big multi document yaml string.
func (v *Provider) concatAllYamlFiles(ctx context.Context, objects []*github.TreeEntry, runevent *info.Event) (string, error) {
	var allTemplates string

	for _, value := range objects {
		if strings.HasSuffix(value.GetPath(), ".yaml") ||
			strings.HasSuffix(value.GetPath(), ".yml") {
			data, err := v.getObject(ctx, value.GetSHA(), runevent)
			if err != nil {
				return "", err
			}
			if err := provider.ValidateYaml(data, value.GetPath()); err != nil {
				return "", err
			}
			if allTemplates != "" && !strings.HasPrefix(string(data), "---") {
				allTemplates += "---"
			}
			allTemplates += "\n" + string(data) + "\n"
		}
	}
	return allTemplates, nil
}

// getPullRequest get a pull request details.
func (v *Provider) getPullRequest(ctx context.Context, runevent *info.Event) (*info.Event, error) {
	pr, _, err := wrapAPI(v, "get_pull_request", func() (*github.PullRequest, *github.Response, error) {
		return v.Client().PullRequests.Get(ctx, runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
	})
	if err != nil {
		return runevent, err
	}
	// Make sure to use the Base for Default BaseBranch or there would be a potential hijack
	runevent.DefaultBranch = pr.GetBase().GetRepo().GetDefaultBranch()
	runevent.URL = pr.GetBase().GetRepo().GetHTMLURL()
	runevent.SHA = pr.GetHead().GetSHA()
	runevent.SHAURL = fmt.Sprintf("%s/commit/%s", pr.GetHTMLURL(), pr.GetHead().GetSHA())
	runevent.PullRequestTitle = pr.GetTitle()

	// TODO: check if we really need this
	if runevent.Sender == "" {
		runevent.Sender = pr.GetUser().GetLogin()
	}
	runevent.HeadBranch = pr.GetHead().GetRef()
	runevent.BaseBranch = pr.GetBase().GetRef()
	runevent.HeadURL = pr.GetHead().GetRepo().GetHTMLURL()
	runevent.BaseURL = pr.GetBase().GetRepo().GetHTMLURL()
	if runevent.EventType == "" {
		runevent.EventType = triggertype.PullRequest.String()
	}

	for _, label := range pr.Labels {
		runevent.PullRequestLabel = append(runevent.PullRequestLabel, label.GetName())
	}

	v.RepositoryIDs = []int64{
		pr.GetBase().GetRepo().GetID(),
	}
	return runevent, nil
}

// GetFiles get a files from pull request.
func (v *Provider) GetFiles(ctx context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	if runevent.TriggerTarget == triggertype.PullRequest {
		opt := &github.ListOptions{PerPage: v.PaginedNumber}
		changedFiles := changedfiles.ChangedFiles{}
		for {
			repoCommit, resp, err := wrapAPI(v, "list_pull_request_files", func() ([]*github.CommitFile, *github.Response, error) {
				return v.Client().PullRequests.ListFiles(ctx, runevent.Organization, runevent.Repository, runevent.PullRequestNumber, opt)
			})
			if err != nil {
				return changedfiles.ChangedFiles{}, err
			}
			for j := range repoCommit {
				changedFiles.All = append(changedFiles.All, *repoCommit[j].Filename)
				if *repoCommit[j].Status == "added" {
					changedFiles.Added = append(changedFiles.Added, *repoCommit[j].Filename)
				}
				if *repoCommit[j].Status == "removed" {
					changedFiles.Deleted = append(changedFiles.Deleted, *repoCommit[j].Filename)
				}
				if *repoCommit[j].Status == "modified" {
					changedFiles.Modified = append(changedFiles.Modified, *repoCommit[j].Filename)
				}
				if *repoCommit[j].Status == "renamed" {
					changedFiles.Renamed = append(changedFiles.Renamed, *repoCommit[j].Filename)
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
		return changedFiles, nil
	}

	if runevent.TriggerTarget == "push" {
		changedFiles := changedfiles.ChangedFiles{}
		rC, _, err := wrapAPI(v, "get_commit_files", func() (*github.RepositoryCommit, *github.Response, error) {
			return v.Client().Repositories.GetCommit(ctx, runevent.Organization, runevent.Repository, runevent.SHA, &github.ListOptions{})
		})
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}
		for i := range rC.Files {
			changedFiles.All = append(changedFiles.All, *rC.Files[i].Filename)
			if *rC.Files[i].Status == "added" {
				changedFiles.Added = append(changedFiles.Added, *rC.Files[i].Filename)
			}
			if *rC.Files[i].Status == "removed" {
				changedFiles.Deleted = append(changedFiles.Deleted, *rC.Files[i].Filename)
			}
			if *rC.Files[i].Status == "modified" {
				changedFiles.Modified = append(changedFiles.Modified, *rC.Files[i].Filename)
			}
			if *rC.Files[i].Status == "renamed" {
				changedFiles.Renamed = append(changedFiles.Renamed, *rC.Files[i].Filename)
			}
		}
		return changedFiles, nil
	}
	return changedfiles.ChangedFiles{}, nil
}

// getObject Get an object from a repository.
func (v *Provider) getObject(ctx context.Context, sha string, runevent *info.Event) ([]byte, error) {
	blob, _, err := wrapAPI(v, "get_blob", func() (*github.Blob, *github.Response, error) {
		return v.Client().Git.GetBlob(ctx, runevent.Organization, runevent.Repository, sha)
	})
	if err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(blob.GetContent())
	if err != nil {
		return nil, err
	}
	return decoded, err
}

// ListRepos lists all the repos for a particular token.
func ListRepos(ctx context.Context, v *Provider) ([]string, error) {
	if v.ghClient == nil {
		return []string{}, fmt.Errorf("no github client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}

	opt := &github.ListOptions{PerPage: v.PaginedNumber}
	repoURLs := []string{}
	for {
		repoList, resp, err := wrapAPI(v, "list_app_repos", func() (*github.ListRepositories, *github.Response, error) {
			return v.Client().Apps.ListRepos(ctx, opt)
		})
		if err != nil {
			return []string{}, err
		}
		for i := range repoList.Repositories {
			repoURLs = append(repoURLs, *repoList.Repositories[i].HTMLURL)
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return repoURLs, nil
}

func (v *Provider) CreateToken(ctx context.Context, repository []string, event *info.Event) (string, error) {
	for _, r := range repository {
		split := strings.Split(r, "/")
		infoData, _, err := wrapAPI(v, "get_repository", func() (*github.Repository, *github.Response, error) {
			return v.Client().Repositories.Get(ctx, split[0], split[1])
		})
		if err != nil {
			v.Logger.Warn("we have an invalid repository: `%s` or no access to it: %v", r, err)
			continue
		}
		v.RepositoryIDs = uniqueRepositoryID(v.RepositoryIDs, infoData.GetID())
	}
	ns := info.GetNS(ctx)
	token, err := v.GetAppToken(ctx, v.Run.Clients.Kube, event.Provider.URL, event.InstallationID, ns)
	if err != nil {
		return "", err
	}
	return token, nil
}

func uniqueRepositoryID(repoIDs []int64, id int64) []int64 {
	r := repoIDs
	m := make(map[int64]bool)
	for _, val := range repoIDs {
		if _, ok := m[val]; !ok {
			m[val] = true
		}
	}
	if _, ok := m[id]; !ok {
		r = append(r, id)
	}
	return r
}

// isHeadCommitOfBranch checks whether provided branch is valid or not and SHA is HEAD commit of the branch.
func (v *Provider) isHeadCommitOfBranch(ctx context.Context, runevent *info.Event, branchName string) error {
	if v.ghClient == nil {
		return fmt.Errorf("no github client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}

	branchInfo, _, err := wrapAPI(v, "get_branch", func() (*github.Branch, *github.Response, error) {
		return v.Client().Repositories.GetBranch(ctx, runevent.Organization, runevent.Repository, branchName, 1)
	})
	if err != nil {
		return err
	}
	if branchInfo.Commit.GetSHA() == runevent.SHA {
		return nil
	}
	return fmt.Errorf("provided SHA %s is not the HEAD commit of the branch %s", runevent.SHA, branchName)
}

func (v *Provider) GetTemplate(commentType provider.CommentType) string {
	return provider.GetHTMLTemplate(commentType)
}

// CreateComment creates a comment on a Pull Request.
func (v *Provider) CreateComment(ctx context.Context, event *info.Event, commit, updateMarker string) error {
	if v.ghClient == nil {
		return fmt.Errorf("no github client has been initialized")
	}

	if event.PullRequestNumber == 0 {
		return fmt.Errorf("create comment only works on pull requests")
	}

	// List last page of the comments of the PR
	if updateMarker != "" {
		comments, _, err := wrapAPI(v, "list_comments", func() ([]*github.IssueComment, *github.Response, error) {
			return v.Client().Issues.ListComments(ctx, event.Organization, event.Repository, event.PullRequestNumber, &github.IssueListCommentsOptions{
				ListOptions: github.ListOptions{
					Page:    1,
					PerPage: 100,
				},
			})
		})
		if err != nil {
			return err
		}

		re := regexp.MustCompile(regexp.QuoteMeta(updateMarker))
		for _, comment := range comments {
			if re.MatchString(comment.GetBody()) {
				if _, _, err := wrapAPI(v, "edit_comment", func() (*github.IssueComment, *github.Response, error) {
					return v.Client().Issues.EditComment(ctx, event.Organization, event.Repository, comment.GetID(), &github.IssueComment{
						Body: &commit,
					})
				}); err != nil {
					return err
				}
				return nil
			}
		}
	}

	_, _, err := wrapAPI(v, "create_comment", func() (*github.IssueComment, *github.Response, error) {
		return v.Client().Issues.CreateComment(ctx, event.Organization, event.Repository, event.PullRequestNumber, &github.IssueComment{
			Body: &commit,
		})
	})
	return err
}
