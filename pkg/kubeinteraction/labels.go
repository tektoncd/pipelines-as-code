package kubeinteraction

import (
	"fmt"
	"strconv"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/versiondata"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const (
	StateStarted   = "started"
	StateQueued    = "queued"
	StateCompleted = "completed"
	StateFailed    = "failed"
)

func AddLabelsAndAnnotations(event *info.Event, pipelineRun *tektonv1.PipelineRun, repo *apipac.Repository, providerConfig *info.ProviderConfig, paramsRun *params.Run) error {
	if event == nil {
		return fmt.Errorf("event should not be nil")
	}
	paramsinfo := paramsRun.Info
	// Add labels on the soon-to-be created pipelinerun so UI/CLI can easily
	// query them.
	labels := map[string]string{
		// These keys are used in LabelSelector query, so we are keeping in Labels as it is.
		// But adding same keys to Annotations so UI/CLI can fetch the actual value instead of modified value
		"app.kubernetes.io/managed-by": pipelinesascode.GroupName,
		"app.kubernetes.io/version":    formatting.CleanValueKubernetes(versiondata.Version),
		keys.URLOrg:                    formatting.CleanValueKubernetes(event.Organization),
		keys.URLRepository:             formatting.CleanValueKubernetes(event.Repository),
		keys.SHA:                       formatting.CleanValueKubernetes(event.SHA),
		keys.Repository:                formatting.CleanValueKubernetes(repo.GetName()),
		keys.State:                     StateStarted,
		keys.EventType:                 formatting.CleanValueKubernetes(event.EventType),
	}

	annotations := map[string]string{
		keys.ShaTitle:      event.SHATitle,
		keys.ShaURL:        event.SHAURL,
		keys.RepoURL:       event.URL,
		keys.SourceRepoURL: event.HeadURL,
		keys.URLOrg:        event.Organization,
		keys.URLRepository: event.Repository,
		keys.SHA:           event.SHA,
		keys.Sender:        event.Sender,
		keys.EventType:     event.EventType,
		keys.Branch:        event.BaseBranch,
		keys.SourceBranch:  event.HeadBranch,
		keys.Repository:    repo.GetName(),
		keys.GitProvider:   providerConfig.Name,
		keys.SecretCreated: "false",
		keys.ControllerInfo: fmt.Sprintf(`{"name":"%s","configmap":"%s","secret":"%s", "gRepo": "%s"}`,
			paramsinfo.Controller.Name, paramsinfo.Controller.Configmap, paramsinfo.Controller.Secret, paramsinfo.Controller.GlobalRepository),
	}

	if event.PullRequestNumber != 0 {
		labels[keys.PullRequest] = strconv.Itoa(event.PullRequestNumber)
		annotations[keys.PullRequest] = strconv.Itoa(event.PullRequestNumber)
	}

	// TODO: move to provider specific function
	if providerConfig.Name == "github" || providerConfig.Name == "github-enterprise" {
		if event.InstallationID > 0 {
			annotations[keys.InstallationID] = strconv.FormatInt(event.InstallationID, 10)
		}
		if event.GHEURL != "" {
			annotations[keys.GHEURL] = event.GHEURL
		}
	}

	// GitLab
	if event.SourceProjectID != 0 {
		annotations[keys.SourceProjectID] = strconv.Itoa(int(event.SourceProjectID))
	}
	if event.TargetProjectID != 0 {
		annotations[keys.TargetProjectID] = strconv.Itoa(int(event.TargetProjectID))
	}

	if event.CloneURL != "" {
		annotations[keys.CloneURL] = event.CloneURL
	}

	if value, ok := pipelineRun.GetObjectMeta().GetAnnotations()[keys.CancelInProgress]; ok {
		labels[keys.CancelInProgress] = value
	}

	for k, v := range labels {
		pipelineRun.Labels[k] = v
	}
	for k, v := range annotations {
		pipelineRun.Annotations[k] = v
	}

	// Add annotations to PipelineRuns to integrate with Tekton Results
	err := AddResultsAnnotation(event, pipelineRun)
	if err != nil {
		return fmt.Errorf("failed to add results annotations with error: %w", err)
	}

	return nil
}
