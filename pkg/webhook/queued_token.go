package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github/app"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/system"
)

const queuedTokenMargin = 5 * time.Minute

func (ac *reconciler) refreshBeforeRelease(ctx context.Context, req *admissionv1.AdmissionRequest, issue func(context.Context, *v1alpha1.Repository, int64, *info.ControllerInfo) (string, time.Time, error)) error {
	if req.Namespace == "" || req.Operation != admissionv1.Update || req.SubResource != "" {
		return nil
	}
	var old, next tektonv1.PipelineRun
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		return err
	}
	if err := json.Unmarshal(req.Object.Raw, &next); err != nil {
		return err
	}
	installation := old.Annotations[keys.InstallationID]
	secretName := old.Annotations[keys.GitAuthSecret]
	if old.Spec.Status != tektonv1.PipelineRunSpecStatusPending || next.Spec.Status != "" ||
		installation == "" || secretName == "" || old.Annotations[keys.GHEURL] != "" {
		return nil
	}
	secrets := ac.client.CoreV1().Secrets(req.Namespace)
	secret, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	recordedExpiry := secret.Annotations[keys.GitHubTokenExpiry]
	if recordedExpiry == "" {
		return nil
	}
	expiry, err := time.Parse(time.RFC3339, recordedExpiry)
	if err != nil {
		return fmt.Errorf("invalid git auth token expiry")
	}
	if expiry.After(time.Now().Add(queuedTokenMargin)) || req.DryRun != nil && *req.DryRun {
		return nil
	}
	if old.UID == "" || !ownedByRun(secret.OwnerReferences, &old) {
		return fmt.Errorf("git auth Secret does not match queued PipelineRun")
	}
	credential, err := url.Parse(strings.TrimSpace(string(secret.Data[".git-credentials"])))
	if err != nil || credential.Scheme != "https" || !strings.EqualFold(credential.Host, "github.com") || credential.User == nil {
		return fmt.Errorf("invalid GitHub clone credential")
	}
	repository, err := ac.pacLister.Repositories(req.Namespace).Get(old.Annotations[keys.Repository])
	if err != nil {
		return fmt.Errorf("get Repository for queued PipelineRun: %w", err)
	}
	repoURL := strings.TrimSuffix(repository.Spec.URL, "/")
	if provider := repository.Spec.GitProvider; provider != nil && (provider.Secret != nil || provider.Type != "" && provider.Type != "github") {
		return fmt.Errorf("repository does not use GitHub App authentication")
	}
	if strings.TrimSuffix(old.Annotations[keys.RepoURL], "/") != repoURL || repository.DeletionTimestamp != nil {
		return fmt.Errorf("queued PipelineRun does not match an active Repository")
	}
	u, err := url.Parse(repoURL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return fmt.Errorf("invalid GitHub Repository URL")
	}
	installationID, err := strconv.ParseInt(installation, 10, 64)
	if err != nil || installationID <= 0 {
		return fmt.Errorf("invalid GitHub installation ID")
	}
	if strings.TrimSuffix(strings.TrimSuffix(credential.Path, "/"), ".git") != strings.TrimSuffix(u.Path, ".git") {
		return fmt.Errorf("git auth Secret URL does not match the Repository")
	}
	controller, err := info.ControllerInfoFromAnnotations(old.GetAnnotations(), nil)
	if err != nil {
		return err
	}
	token, expiry, err := issue(ctx, repository, installationID, controller)
	if err != nil {
		return err
	}
	credential.User = url.UserPassword(credential.User.Username(), token)
	secret.Data[".git-credentials"] = []byte(credential.String())
	secret.Data["git-provider-token"] = []byte(token)
	secret.Annotations[keys.GitHubTokenExpiry] = expiry.UTC().Format(time.RFC3339)
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func ownedByRun(refs []metav1.OwnerReference, run *tektonv1.PipelineRun) bool {
	for _, ref := range refs {
		if ref.Kind == "PipelineRun" && ref.Name == run.Name && ref.UID == run.UID {
			return true
		}
	}
	return false
}

func (ac *reconciler) issueQueuedToken(ctx context.Context, repo *v1alpha1.Repository, installationID int64, controller *info.ControllerInfo) (string, time.Time, error) {
	run := params.New()
	run.Info.Controller = controller
	run.Clients.Kube = ac.client
	run.RepositoryLister = ac.pacLister
	ns := system.Namespace()
	ctx = info.StoreNS(ctx, ns)
	logger := logging.FromContext(ctx).With("provider", "github", "repository", repo.Spec.URL)
	config, err := ac.client.CoreV1().ConfigMaps(ns).Get(ctx, run.Info.Controller.Configmap, metav1.GetOptions{})
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := run.Info.UpdatePacOpts(logger, config.Data); err != nil {
		return "", time.Time{}, err
	}

	gh := github.New()
	gh.Run = run
	gh.SetLogger(logger)
	pacOpts := run.Info.GetPacOpts()
	gh.SetPacInfo(&pacOpts)
	host, bootstrapToken, id, err := app.NewInstallation(run, repo, gh, ns).GetAndUpdateInstallationID(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if id != installationID {
		return "", time.Time{}, fmt.Errorf("GitHub installation does not match the Repository")
	}
	if bootstrapToken == "" {
		return "", time.Time{}, fmt.Errorf("could not initialize GitHub installation client")
	}

	global, err := run.GetRepository(ctx, ns, run.Info.Controller.GlobalRepository)
	if err == nil {
		var merged v1alpha1.Repository
		repo.DeepCopyInto(&merged)
		merged.Spec.Merge(global.Spec)
		repo = &merged
	} else if !errors.IsNotFound(err) {
		return "", time.Time{}, err
	}

	org, name, err := formatting.GetRepoOwnerSplitted(strings.TrimSuffix(repo.Spec.URL, "/"))
	if err != nil {
		return "", time.Time{}, err
	}
	event := info.NewEvent()
	event.Organization = org
	event.Repository = name
	event.URL = repo.Spec.URL
	event.GHEURL = host
	event.InstallationID = id
	event.Provider.URL = host
	event.Provider.Token = bootstrapToken
	gh.RepositoryNames = []string{name}
	if err := gh.SetClient(ctx, run, event, repo, events.NewEventEmitter(nil, logger)); err != nil {
		return "", time.Time{}, fmt.Errorf("cannot scope GitHub token: %w", err)
	}
	if event.Provider.Token == "" || !event.Provider.GitHubTokenExpiresAt.After(time.Now().Add(queuedTokenMargin)) {
		return "", time.Time{}, fmt.Errorf("GitHub returned no usable token")
	}
	return event.Provider.Token, event.Provider.GitHubTokenExpiresAt, nil
}
