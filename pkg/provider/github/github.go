package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/go-github/v81/github"
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
	triggerEvent       string
	cachedChangedFiles *changedfiles.ChangedFiles
	commitInfo         *github.Commit
	pacUserLogin       string // user/bot login used by PAC
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

	return nil
}

// GetTektonDir retrieves all YAML files from the .tekton directory and returns them as a single concatenated multi-document YAML file.
func (v *Provider) GetTektonDir(ctx context.Context, runevent *info.Event, path, provenance string) (string, error) {
	tektonDirSha := ""

	v.provenance = provenance
	// default set provenance from the SHA
	revision := runevent.SHA
	if provenance == "default_branch" {
		v.Logger.Infof("Using PipelineRun definition from default_branch: %s", runevent.DefaultBranch)
		branch, _, err := wrapAPI(v, "get_default_branch", func() (*github.Branch, *github.Response, error) {
			return v.Client().Repositories.GetBranch(ctx, runevent.Organization, runevent.Repository, runevent.DefaultBranch, 1)
		})
		if err != nil {
			return "", err
		}
		revision = branch.GetCommit().GetSHA()
		if revision == "" {
			return "", fmt.Errorf("default_branch %s did not resolve to a commit SHA", runevent.DefaultBranch)
		}
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
	return v.concatAllYamlFiles(ctx, tektonDirObjects.Entries, runevent, path, revision)
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

	// check if the commit info is already cached in provider
	if v.commitInfo == nil {
		commit, _, err = wrapAPI(v, "get_commit", func() (*github.Commit, *github.Response, error) {
			return v.Client().Git.GetCommit(ctx, runevent.Organization, runevent.Repository, sha)
		})
		if err != nil {
			return err
		}
	} else {
		commit = v.commitInfo
	}

	runevent.SHAURL = commit.GetHTMLURL()
	runevent.SHATitle = strings.Split(commit.GetMessage(), "\n\n")[0]
	runevent.SHA = commit.GetSHA()
	runevent.HasSkipCommand = provider.SkipCI(commit.GetMessage())

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
// tektonDirPath is the path to the .tekton directory (e.g., ".tekton") used to construct full paths.
func (v *Provider) concatAllYamlFiles(ctx context.Context, objects []*github.TreeEntry, runevent *info.Event, tektonDirPath, ref string) (string, error) {
	// Collect all YAML file paths and preserve order
	// Tree entries have paths relative to the .tekton directory, so prepend tektonDirPath
	var yamlFiles []string
	for _, value := range objects {
		if strings.HasSuffix(value.GetPath(), ".yaml") ||
			strings.HasSuffix(value.GetPath(), ".yml") {
			// Construct full path from repo root for GraphQL query
			fullPath := tektonDirPath + "/" + value.GetPath()
			yamlFiles = append(yamlFiles, fullPath)
		}
	}

	if len(yamlFiles) == 0 {
		return "", nil
	}

	client, err := newGraphQLClient(v)
	if err != nil {
		return "", fmt.Errorf("failed to create GraphQL client: %w", err)
	}
	graphQLResults, err := client.fetchFiles(ctx, runevent.Organization, runevent.Repository, ref, yamlFiles)
	if err != nil {
		return "", fmt.Errorf("failed to fetch .tekton files via GraphQL: %w", err)
	}

	var allTemplates string

	for _, path := range yamlFiles {
		content, ok := graphQLResults[path]
		if !ok {
			return "", fmt.Errorf("file %s not found in GraphQL response", path)
		}

		// it used to be like that (stripped prefix) before we moved to GraphQL so
		// let's keep it that way.
		relativePath := strings.TrimPrefix(path, tektonDirPath+"/")
		if err := provider.ValidateYaml(content, relativePath); err != nil {
			return "", err
		}
		if allTemplates != "" && !strings.HasPrefix(string(content), "---") {
			allTemplates += "---"
		}
		allTemplates += "\n" + string(content) + "\n"
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

// GetFiles gets and caches the list of files changed by a given event.
func (v *Provider) GetFiles(ctx context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	if v.cachedChangedFiles == nil {
		changes, err := v.fetchChangedFiles(ctx, runevent)
		if err != nil {
			return changedfiles.ChangedFiles{}, err
		}
		v.cachedChangedFiles = &changes
	}
	return *v.cachedChangedFiles, nil
}

func (v *Provider) fetchChangedFiles(ctx context.Context, runevent *info.Event) (changedfiles.ChangedFiles, error) {
	changedFiles := changedfiles.ChangedFiles{}

	switch runevent.TriggerTarget {
	case triggertype.PullRequest:
		opt := &github.ListOptions{PerPage: v.PaginedNumber}
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
	case triggertype.Push:
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
	default:
		// No action necessary
	}
	return changedFiles, nil
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
	var appReposCache []*github.Repository

	for _, r := range repository {
		// Check if this is a glob pattern
		if strings.ContainsAny(r, "*?[") {
			if err := v.expandGlobAndAddRepoIDs(ctx, r, &appReposCache); err != nil {
				v.Logger.Warn("failed to expand glob pattern %q: %v", r, err)
			}
			continue
		}

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

func (v *Provider) expandGlobAndAddRepoIDs(ctx context.Context, repoPattern string, cache *[]*github.Repository) error {
	// We can skip error check here as all the glob compilation has been checked
	// before this method is called.
	reposToScope, _ := glob.Compile(repoPattern)

	if *cache == nil {
		repos, err := v.listAppRepos(ctx)
		if err != nil {
			return err
		}
		*cache = repos
	}

	for _, repo := range *cache {
		repoFullName := repo.GetFullName()
		if reposToScope.Match(repoFullName) {
			v.RepositoryIDs = uniqueRepositoryID(v.RepositoryIDs, repo.GetID())
		}
	}

	return nil
}

func (v *Provider) listAppRepos(ctx context.Context) ([]*github.Repository, error) {
	var allRepos []*github.Repository

	opt := &github.ListOptions{PerPage: v.PaginedNumber}
	for {
		repoList, resp, err := wrapAPI(v, "list_app_repos", func() (*github.ListRepositories, *github.Response, error) {
			return v.Client().Apps.ListRepos(ctx, opt)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list app repos: %w", err)
		}

		allRepos = append(allRepos, repoList.Repositories...)

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return allRepos, nil
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

type commentTraceLogContext struct {
	dedupTrace      string
	eventID         string
	markerHash      string
	markerLen       int
	controllerLabel string
}

func newDedupTraceID() string {
	//nolint:gosec // best-effort correlation ID for debug logs only
	return fmt.Sprintf("%x-%04x", time.Now().UnixNano(), rand.Intn(1<<16))
}

func markerHash(marker string) string {
	if marker == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(marker))
	digest := hex.EncodeToString(sum[:])
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func formatCommentTime(ts github.Timestamp) string {
	if ts.IsZero() {
		return "unknown"
	}
	return ts.UTC().Format(time.RFC3339)
}

func compactCommentIDs(comments []*github.IssueComment) []string {
	out := make([]string, 0, len(comments))
	for _, comment := range comments {
		out = append(out, fmt.Sprintf("%d@%s", comment.GetID(), formatCommentTime(comment.GetCreatedAt())))
	}
	return out
}

func responseStatusCode(resp *github.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func githubRequestID(resp *github.Response) string {
	if resp == nil || resp.Response == nil {
		return ""
	}
	return resp.Header.Get("X-GitHub-Request-Id")
}

func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:4])
}

func eventID(event *info.Event) string {
	if event == nil || event.Request == nil {
		return "unknown"
	}
	if id := event.Request.Header.Get("X-GitHub-Delivery"); id != "" {
		return id
	}
	return "unknown"
}

func (v *Provider) controllerLabel(ctx context.Context) string {
	if name := info.GetCurrentControllerName(ctx); name != "" {
		return name
	}
	if v.Run != nil && v.Run.Info.Controller != nil && v.Run.Info.Controller.Name != "" {
		return v.Run.Info.Controller.Name
	}
	return "unknown"
}

func (v *Provider) newCommentTraceLogContext(ctx context.Context, event *info.Event, marker string) commentTraceLogContext {
	return commentTraceLogContext{
		dedupTrace:      newDedupTraceID(),
		eventID:         eventID(event),
		markerHash:      markerHash(marker),
		markerLen:       len(marker),
		controllerLabel: v.controllerLabel(ctx),
	}
}

func (v *Provider) debugCommentPhase(event *info.Event, trace commentTraceLogContext, phase string, kv ...any) {
	if v.Logger == nil {
		return
	}

	org := "unknown"
	repo := "unknown"
	pr := 0
	if event != nil {
		org = event.Organization
		repo = event.Repository
		pr = event.PullRequestNumber
	}

	baseFields := make([]any, 0, 18+len(kv))
	baseFields = append(baseFields,
		"phase", phase,
		"organization", org,
		"repository", repo,
		"pr", pr,
		"event_id", trace.eventID,
		"dedup_trace", trace.dedupTrace,
		"marker_hash", trace.markerHash,
		"marker_len", trace.markerLen,
		"controller_label", trace.controllerLabel,
	)
	v.Logger.Debugw("github comment dedup flow", append(baseFields, kv...)...)
}

func (v *Provider) listCommentsByMarker(
	ctx context.Context,
	event *info.Event,
	marker, phase string,
	trace commentTraceLogContext,
) ([]*github.IssueComment, error) {
	comments, _, err := wrapAPI(v, "list_comments", func() ([]*github.IssueComment, *github.Response, error) {
		return v.Client().Issues.ListComments(ctx, event.Organization, event.Repository, event.PullRequestNumber, &github.IssueListCommentsOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: v.PaginedNumber,
			},
		})
	})
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(regexp.QuoteMeta(marker))
	matchedComments := make([]*github.IssueComment, 0, len(comments))
	for _, comment := range comments {
		if re.MatchString(comment.GetBody()) {
			if err = v.getUserLogin(ctx, event); err != nil {
				return nil, fmt.Errorf("unable to fetch user info: %w", err)
			}
			// Only edit comments created by this PAC installation's credentials.
			// Prevents accidentally modifying comments from other users/bots.
			if comment.GetUser().GetLogin() != v.pacUserLogin {
				v.Logger.Debugf("This comment was not created by PAC, skipping comment edit :%d, created by user %s, PAC user: %s",
					comment.GetID(), comment.GetUser().GetLogin(), v.pacUserLogin)
				continue
			}
			matchedComments = append(matchedComments, comment)
		}
	}

	if len(comments) == v.PaginedNumber {
		v.debugCommentPhase(event, trace, phase+"_pagination_warning",
			"fetched_count", len(comments),
			"note", "response returned exactly PerPage comments; marker matches beyond page 1 may be missed",
		)
	}

	v.debugCommentPhase(event, trace, phase,
		"fetched_count", len(comments),
		"matched_count", len(matchedComments),
		"matched_comments", compactCommentIDs(matchedComments),
	)

	return matchedComments, nil
}

// CreateComment creates a comment on a Pull Request.
func (v *Provider) CreateComment(ctx context.Context, event *info.Event, commit, updateMarker string) error {
	if v.ghClient == nil {
		return fmt.Errorf("no github client has been initialized")
	}
	if event.PullRequestNumber == 0 {
		return fmt.Errorf("create comment only works on pull requests")
	}

	trace := v.newCommentTraceLogContext(ctx, event, updateMarker)

	if updateMarker != "" {
		existingComments, err := v.listCommentsByMarker(ctx, event, updateMarker, "initial_list", trace)
		if err != nil {
			return err
		}

		if len(existingComments) > 1 {
			v.debugCommentPhase(event, trace, "duplicate_detected",
				"matched_count", len(existingComments),
				"matched_comments", compactCommentIDs(existingComments),
			)
		}

		if len(existingComments) > 0 {
			comment := existingComments[0]
			if comment.GetBody() == commit {
				v.debugCommentPhase(event, trace, "no_edit_needed",
					"comment_id", comment.GetID(),
					"body_hash", bodyHash(commit))
				return nil
			}
			v.debugCommentPhase(event, trace, "edit_comment",
				"comment_id", comment.GetID(),
				"body_hash", bodyHash(commit))
			if _, _, err := wrapAPI(v, "edit_comment", func() (*github.IssueComment, *github.Response, error) {
				return v.Client().Issues.EditComment(ctx, event.Organization, event.Repository, comment.GetID(), &github.IssueComment{
					Body: github.Ptr(commit),
				})
			}); err != nil {
				return err
			}
			return nil
		}
	} else {
		v.debugCommentPhase(event, trace, "no_marker",
			"body_hash", bodyHash(commit))
	}

	v.debugCommentPhase(event, trace, "create_comment_start",
		"body_hash", bodyHash(commit))
	createdComment, createResp, err := wrapAPI(v, "create_comment", func() (*github.IssueComment, *github.Response, error) {
		return v.Client().Issues.CreateComment(ctx, event.Organization, event.Repository, event.PullRequestNumber, &github.IssueComment{
			Body: github.Ptr(commit),
		})
	})
	if err != nil {
		v.debugCommentPhase(event, trace, "create_comment_done",
			"status_code", responseStatusCode(createResp),
			"github_request_id", githubRequestID(createResp),
			"create_error", err.Error(),
		)
		return err
	}
	v.debugCommentPhase(event, trace, "create_comment_done",
		"status_code", responseStatusCode(createResp),
		"github_request_id", githubRequestID(createResp),
		"created_comment_id", createdComment.GetID(),
	)
	return nil
}

func (v *Provider) getUserLogin(ctx context.Context, event *info.Event) error {
	if v.pacUserLogin != "" {
		return nil
	}

	// Get the PAC user/bot login.
	if event.InstallationID > 0 {
		// For Apps, get the app slug.
		slug, err := v.fetchAppSlug(ctx, event.Provider.URL)
		if err != nil {
			return fmt.Errorf("failed to fetch app slug: %w", err)
		}

		v.pacUserLogin = fmt.Sprintf("%s[bot]", slug)
	} else {
		// For PATs, get the authenticated user.
		pacUser, _, err := v.Client().Users.Get(ctx, "")
		if err != nil {
			return fmt.Errorf("unable to fetch user info: %w", err)
		}
		v.pacUserLogin = pacUser.GetLogin()
	}

	return nil
}

// GenerateJWT generates a JWT token for GitHub App.
// It retrieves the application ID and private key, sets the claims, and signs the token.
func (v *Provider) GenerateJWT(ctx context.Context, ns string, kube kubernetes.Interface) (string, error) {
	applicationID, privateKey, err := v.GetAppIDAndPrivateKey(ctx, ns, kube)
	if err != nil {
		return "", err
	}

	parsedPK, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// The expirationTime claim identifies the expiration time on or after which the JWT MUST NOT be accepted for processing.
	// Maximum allowed duration is 10 minutes, we use 5 minutes for safety.
	// See https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
	now := time.Now()
	claims := &jwt.RegisteredClaims{
		Issuer:    fmt.Sprintf("%d", applicationID),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(parsedPK)
	if err != nil {
		return "", fmt.Errorf("failed to sign private key: %w", err)
	}

	return tokenString, nil
}

// Fetch the app slug used for identifying the application.
func (v *Provider) fetchAppSlug(ctx context.Context, apiURL string) (string, error) {
	ns := info.GetNS(ctx)
	tokenString, err := v.GenerateJWT(ctx, ns, v.Run.Clients.Kube)
	if err != nil {
		return "", err
	}

	client, _, _ := MakeClient(ctx, apiURL, tokenString)
	app, _, err := client.Apps.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("failed to get app info: %w", err)
	}

	return app.GetSlug(), nil
}
