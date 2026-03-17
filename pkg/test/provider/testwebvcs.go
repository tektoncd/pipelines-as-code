package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
)

var _ provider.Interface = (*TestProviderImp)(nil)

type TestProviderImp struct {
	AllowIT                bool
	Event                  *info.Event
	TektonDirTemplate      string
	CreateStatusErorring   bool
	FilesInsideRepo        map[string]string
	WantProviderRemoteTask bool
	PolicyDisallowing      bool
	AllowedInOwnersFile    bool
	WantAllChangedFiles    []string
	WantAddedFiles         []string
	WantDeletedFiles       []string
	WantModifiedFiles      []string
	WantRenamedFiles       []string
	FailGetCommitInfo      bool
	CommitInfoErrorMsg     string
	pacInfo                *info.PacOpts
	CommitStatuses         []provider.CommitStatusInfo
}

func (v *TestProviderImp) SetPacInfo(pacInfo *info.PacOpts) {
	v.pacInfo = pacInfo
}

func (v *TestProviderImp) CheckPolicyAllowing(_ context.Context, _ *info.Event, _ []string) (bool, string) {
	if v.PolicyDisallowing {
		return false, "policy disallowing"
	}
	return true, ""
}

func (v *TestProviderImp) IsAllowedOwnersFile(_ context.Context, _ *info.Event) (bool, error) {
	return v.AllowedInOwnersFile, nil
}

func (v *TestProviderImp) CreateComment(_ context.Context, _ *info.Event, _, _ string) error {
	return nil
}

func (v *TestProviderImp) SetLogger(_ *zap.SugaredLogger) {
}

func (v *TestProviderImp) Validate(_ context.Context, _ *params.Run, _ *info.Event) error {
	return nil
}

func (v *TestProviderImp) Detect(_ *http.Request, _ string, _ *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	return true, true, nil, "", nil
}

func (v *TestProviderImp) ParsePayload(_ context.Context, _ *params.Run, _ *http.Request, _ string) (*info.Event, error) {
	return v.Event, nil
}

func (v *TestProviderImp) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{}
}

func (v *TestProviderImp) GetCommitInfo(_ context.Context, event *info.Event) error {
	if v.FailGetCommitInfo {
		if v.CommitInfoErrorMsg != "" {
			return fmt.Errorf("%s", v.CommitInfoErrorMsg)
		}
		return fmt.Errorf("failed to get commit info")
	}
	// Simulate what real providers do: set HasSkipCommand based on commit message
	// Real providers set this from the commit message fetched via API
	if event.SHATitle != "" {
		event.HasSkipCommand = provider.SkipCI(event.SHATitle)
	}
	return nil
}

func (v *TestProviderImp) SetClient(_ context.Context, _ *params.Run, _ *info.Event, _ *v1alpha1.Repository, _ *events.EventEmitter) error {
	return nil
}

func (v *TestProviderImp) IsAllowed(_ context.Context, _ *info.Event) (bool, error) {
	if v.AllowIT {
		return true, nil
	}
	return false, nil
}

func (v *TestProviderImp) GetTaskURI(_ context.Context, _ *info.Event, _ string) (bool, string, error) {
	return v.WantProviderRemoteTask, "", nil
}

func (v *TestProviderImp) CreateStatus(_ context.Context, _ *info.Event, _ providerstatus.StatusOpts) error {
	if v.CreateStatusErorring {
		return fmt.Errorf("some provider error occurred while reporting status")
	}
	return nil
}

func (v *TestProviderImp) GetTektonDir(_ context.Context, _ *info.Event, _, _ string) (string, error) {
	return v.TektonDirTemplate, nil
}

func (v *TestProviderImp) GetFileInsideRepo(_ context.Context, _ *info.Event, file, _ string) (string, error) {
	if val, ok := v.FilesInsideRepo[file]; ok {
		return val, nil
	}
	return "", fmt.Errorf("could not find %s in tests", file)
}

func (v *TestProviderImp) GetFiles(_ context.Context, _ *info.Event) (changedfiles.ChangedFiles, error) {
	if v == nil {
		return changedfiles.ChangedFiles{}, nil
	}
	return changedfiles.ChangedFiles{
		All:      v.WantAllChangedFiles,
		Added:    v.WantAddedFiles,
		Deleted:  v.WantDeletedFiles,
		Modified: v.WantModifiedFiles,
		Renamed:  v.WantRenamedFiles,
	}, nil
}

func (v *TestProviderImp) CreateToken(_ context.Context, _ []string, _ *info.Event) (string, error) {
	return "", nil
}

func (v *TestProviderImp) GetTemplate(commentType provider.CommentType) string {
	return provider.GetHTMLTemplate(commentType)
}

func (v *TestProviderImp) GetCommitStatuses(_ context.Context, _ *info.Event) ([]provider.CommitStatusInfo, error) {
	return v.CommitStatuses, nil
}
