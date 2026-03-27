package provider

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"gotest.tools/v3/assert"
)

func TestIsOkToTestComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    bool
	}{
		{
			name:    "valid",
			comment: "/ok-to-test",
			want:    true,
		},
		{
			name:    "valid with some string before",
			comment: "/lgtm \n/ok-to-test",
			want:    true,
		},
		{
			name:    "valid with some string before and after",
			comment: "hi, trigger the ci \n/ok-to-test \n then report the status back",
			want:    true,
		},
		{
			name:    "valid comments",
			comment: "/lgtm \n/ok-to-test \n/approve",
			want:    true,
		},
		{
			name:    "invalid",
			comment: "/ok",
			want:    false,
		},
		{
			name:    "invalid comment",
			comment: "/ok-to-test abc",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOkToTestComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCancelComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    bool
	}{
		{
			name:    "valid",
			comment: "/cancel",
			want:    true,
		},
		{
			name:    "valid with some string before",
			comment: "/lgtm \n/cancel",
			want:    true,
		},
		{
			name:    "valid with some string before and after",
			comment: "hi, trigger the ci \n/cancel \n then report the status back",
			want:    true,
		},
		{
			name:    "valid comments",
			comment: "/lgtm \n/cancel \n/approve",
			want:    true,
		},
		{
			name:    "invalid",
			comment: "/ok",
			want:    false,
		},
		{
			name:    "invalid comment",
			comment: "/ok-to-test abc",
			want:    false,
		},
		{
			name:    "cancel single pr",
			comment: "/cancel abc",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCancelComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsTestRetestComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    bool
	}{
		{
			name:    "valid retest",
			comment: "/retest",
			want:    true,
		},
		{
			name:    "valid test",
			comment: "/test",
			want:    true,
		},
		{
			name:    "valid retest with some string before",
			comment: "/lgtm \n/retest",
			want:    true,
		},
		{
			name:    "valid test with some string before",
			comment: "/lgtm \n/test",
			want:    true,
		},
		{
			name:    "valid with some string before and after",
			comment: "hi, trigger the ci \n/retest \n then report the status back",
			want:    true,
		},
		{
			name:    "test valid with some string before and after",
			comment: "hi, trigger the ci \n/test \n then report the status back",
			want:    true,
		},
		{
			name:    "valid comments",
			comment: "/lgtm \n/retest \n/approve",
			want:    true,
		},
		{
			name:    "invalid",
			comment: "/ok",
			want:    false,
		},
		{
			name:    "retest trigger single pr",
			comment: "/retest abc",
			want:    true,
		},
		{
			name:    "test trigger single pr",
			comment: "/test abc",
			want:    true,
		},
		{
			name:    "invalid",
			comment: "test abc",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTestRetestComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPipelineRunFromComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{
			name:    "test no pipelinerun",
			comment: "/test",
			want:    "",
		},
		{
			name:    "retest no pipelinerun",
			comment: "/retest",
			want:    "",
		},
		{
			name:    "test a pipeline",
			comment: "/test abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string before test command",
			comment: "abc \n /test abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string after test command",
			comment: "/test abc-01-pr \n abc",
			want:    "abc-01-pr",
		},
		{
			name:    "string before and after test command",
			comment: "before \n /test abc-01-pr \n after",
			want:    "abc-01-pr",
		},
		{
			name:    "retest a pipeline",
			comment: "/retest abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string before retest command",
			comment: "abc \n /retest abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string after retest command",
			comment: "/retest abc-01-pr \n abc",
			want:    "abc-01-pr",
		},
		{
			name:    "string before and after retest command",
			comment: "before \n /retest abc-01-pr \n after",
			want:    "abc-01-pr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPipelineRunFromTestComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPipelineRunFromCancelComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{
			name:    "cancel all",
			comment: "/cancel",
			want:    "",
		},
		{
			name:    "cancel a pipeline",
			comment: "/cancel abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string before cancel command",
			comment: "abc \n /cancel abc-01-pr",
			want:    "abc-01-pr",
		},
		{
			name:    "string after cancel command",
			comment: "/cancel abc-01-pr \n abc",
			want:    "abc-01-pr",
		},
		{
			name:    "string before and after cancel command",
			comment: "before \n /cancel abc-01-pr \n after",
			want:    "abc-01-pr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPipelineRunFromCancelComment(tt.comment)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPipelineRunAndBranchNameFromTestComment(t *testing.T) {
	tests := []struct {
		name       string
		comment    string
		branchName string
		prName     string
		prefix     string
		wantError  bool
	}{
		{
			name:       "retest all on test branch",
			comment:    "/retest branch:test",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "test a pipeline on test branch",
			comment:    "/test abc-01-pr branch:test",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for test command before branch name test",
			comment:    "/test abc-01-pr abc \n branch:test",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for retest command after branch name test",
			comment:    "/retest abc-01-pr branch:test \n abc",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for test command before and after branch name test",
			comment:    "/test abc-01-pr \n before branch:test \n after",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "branch name contains /test",
			comment:    "/test abc-01-pr abc \n branch:chore/test",
			prName:     "abc-01-pr",
			branchName: "chore/test",
			wantError:  false,
		},
		{
			name:       "branch name contains substring tag so not parsed as tag",
			comment:    "/retest my-pipeline branch:feature-test-tagged-release",
			prName:     "my-pipeline",
			branchName: "feature-test-tagged-release",
			wantError:  false,
		},
		{
			name:      "different word other than branch for retest command",
			comment:   "/retest invalidname:nightly",
			wantError: true,
		},
		{
			name:      "test all",
			comment:   "/test",
			wantError: false,
		},
		{
			name:      "test a pipeline",
			comment:   "/test abc-01-pr",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:       "test a pipeline with key value",
			comment:    "/test abc-01-pr key=value",
			prName:     "abc-01-pr",
			branchName: "",
			wantError:  false,
		},
		{
			name:      "string before retest command",
			comment:   "abc \n /retest abc-01-pr",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:      "string after retest command",
			comment:   "/retest abc-01-pr \n abc",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:      "string before and after test command",
			comment:   "before \n /test abc-01-pr \n after",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:       "test a pipeline with prefix",
			comment:    "/pac-test abc-01-pr",
			prName:     "abc-01-pr",
			branchName: "",
			prefix:     "/pac-",
			wantError:  false,
		},
		{
			name:       "retest a pipeline with prefix",
			comment:    "/pac-retest abc-01-pr",
			prName:     "abc-01-pr",
			branchName: "",
			prefix:     "/pac-",
			wantError:  false,
		},
		{
			name:       "test a pipeline with prefix and key value",
			comment:    "/pac-test abc-01-pr key=value",
			prName:     "abc-01-pr",
			branchName: "",
			prefix:     "/pac-",
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			prName, branchName, _, err := GetPipelineRunAndBranchOrTagNameFromTestComment(tt.comment, tt.prefix)
			assert.Equal(t, tt.wantError, err != nil)
			assert.Equal(t, tt.branchName, branchName)
			assert.Equal(t, tt.prName, prName)
		})
	}
}

func TestGetPipelineRunAndBranchNameFromCancelComment(t *testing.T) {
	tests := []struct {
		name       string
		comment    string
		branchName string
		prName     string
		prefix     string
		wantError  bool
	}{
		{
			name:      "cancel all pipeline",
			comment:   "/cancel",
			wantError: false,
		},
		{
			name:      "cancel a particular pipeline",
			comment:   "/cancel abc-01-pr",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:      "add string before cancel command",
			comment:   "abc \n /cancel abc-01-pr",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:      "add string after cancel command",
			comment:   "/cancel abc-01-pr \n abc",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:      "add string before and after cancel command",
			comment:   "before \n /cancel abc-01-pr \n after",
			prName:    "abc-01-pr",
			wantError: false,
		},
		{
			name:       "cancel all on test branch",
			comment:    "/cancel branch:test",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "cancel a pipeline on test branch",
			comment:    "/cancel abc-01-pr branch:test",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for cancel command before branch name test",
			comment:    "/cancel abc-01-pr abc \n branch:test",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for cancel command after branch name test",
			comment:    "/cancel abc-01-pr branch:test \n abc",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:       "string for cancel command before and after branch name test",
			comment:    "/cancel abc-01-pr \n before branch:test \n after",
			prName:     "abc-01-pr",
			branchName: "test",
			wantError:  false,
		},
		{
			name:      "different word other than branch for cancel command",
			comment:   "/cancel invalidname:nightly",
			wantError: true,
		},
		{
			name:      "cancel all with prefix",
			comment:   "/pac-cancel",
			prefix:    "/pac-",
			wantError: false,
		},
		{
			name:      "cancel single with prefix",
			comment:   "/pac-cancel abc-pr",
			prName:    "abc-pr",
			prefix:    "/pac-",
			wantError: false,
		},
		{
			name:       "cancel with branch and prefix",
			comment:    "/pac-cancel branch:test",
			branchName: "test",
			prefix:     "/pac-",
			wantError:  false,
		},
		{
			name:       "cancel single with branch and prefix",
			comment:    "/pac-cancel abc-pr branch:test",
			prName:     "abc-pr",
			branchName: "test",
			prefix:     "/pac-",
			wantError:  false,
		},
		{
			name:      "different prefix cancel single",
			comment:   "/myteam-cancel xyz-pr",
			prName:    "xyz-pr",
			prefix:    "/myteam-",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			prName, branchName, _, err := GetPipelineRunAndBranchOrTagNameFromCancelComment(tt.comment, tt.prefix)
			assert.Equal(t, tt.wantError, err != nil)
			assert.Equal(t, tt.branchName, branchName)
			assert.Equal(t, tt.prName, prName)
		})
	}
}

func TestCompareHostOfURLS(t *testing.T) {
	tests := []struct {
		name string
		url1 string
		url2 string
		want bool
	}{
		{
			name: "exact same",
			url1: "https://shivam.com/foo/bar",
			url2: "https://shivam.com/hello/moto",
			want: true,
		},
		{
			name: "exact same but different",
			url1: "https://shivam.com/foo/bar",
			url2: "https://vincent.com/foo/bar",
			want: false,
		},
		{
			name: "bad url1",
			url1: "i am such a bad url",
			want: false,
		},
		{
			name: "bad url2",
			url2: "i am the baddest, choose me!",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareHostOfURLS(tt.url1, tt.url2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetCheckName(t *testing.T) {
	type args struct {
		status  status.StatusOpts
		pacopts *info.PacOpts
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "no application name",
			args: args{
				status: status.StatusOpts{
					OriginalPipelineRunName: "HELLO",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{ApplicationName: ""}},
			},
			want: "HELLO",
		},
		{
			name: "application and pipelinerun name",
			args: args{
				status: status.StatusOpts{
					OriginalPipelineRunName: "MOTO",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{ApplicationName: "HELLO"}},
			},
			want: "HELLO / MOTO",
		},
		{
			name: "application no pipelinerun name",
			args: args{
				status: status.StatusOpts{
					OriginalPipelineRunName: "",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{ApplicationName: "PAC"}},
			},
			want: "PAC",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetCheckName(tt.args.status, tt.args.pacopts); got != tt.want {
				t.Errorf("GetCheckName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSkipCI(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		want          bool
	}{
		{
			name:          "skip ci lowercase",
			commitMessage: "fix: some bug [skip ci]",
			want:          true,
		},
		{
			name:          "ci skip lowercase",
			commitMessage: "feat: new feature [ci skip]",
			want:          true,
		},
		{
			name:          "skip tkn lowercase",
			commitMessage: "docs: update readme [skip tkn]",
			want:          true,
		},
		{
			name:          "tkn skip lowercase",
			commitMessage: "chore: update deps [tkn skip]",
			want:          true,
		},
		{
			name:          "skip ci at beginning",
			commitMessage: "[skip ci] WIP: work in progress",
			want:          true,
		},
		{
			name:          "ci skip in middle",
			commitMessage: "fix: bug\n\n[ci skip]\n\nmore details",
			want:          true,
		},
		{
			name:          "skip tkn at end",
			commitMessage: "feat: new feature\n\nSome description [skip tkn]",
			want:          true,
		},
		{
			name:          "multiple skip commands",
			commitMessage: "fix: bug [skip ci] [skip tkn]",
			want:          true,
		},
		{
			name:          "no skip command",
			commitMessage: "feat: normal commit without skip",
			want:          false,
		},
		{
			name:          "skip ci with typo",
			commitMessage: "fix: bug [skip-ci]",
			want:          false,
		},
		{
			name:          "skip without brackets",
			commitMessage: "fix: bug skip ci",
			want:          false,
		},
		{
			name:          "empty commit message",
			commitMessage: "",
			want:          false,
		},
		{
			name:          "skip ci with uppercase",
			commitMessage: "fix: bug [SKIP CI]",
			want:          false,
		},
		{
			name:          "skip ci with extra spaces",
			commitMessage: "fix: bug [ skip ci ]",
			want:          false,
		},
		{
			name:          "multiline with skip ci",
			commitMessage: "fix: important bug fix\n\nThis commit fixes a critical issue.\n\n[skip ci]",
			want:          true,
		},
		{
			name:          "skip ci in commit body",
			commitMessage: "feat: add new feature\n\nTesting [skip ci] in body",
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SkipCI(tt.commitMessage)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetGitOpsCommentPrefix(t *testing.T) {
	tests := []struct {
		name string
		repo *v1alpha1.Repository
		want string
	}{
		{
			name: "no settings returns default prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: nil,
				},
			},
			want: "/",
		},
		{
			name: "empty GitOpsCommandPrefix returns default prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "",
					},
				},
			},
			want: "/",
		},
		{
			name: "custom prefix present in comment returns custom prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "custom prefix not present in comment returns default prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "custom prefix with retest command",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "custom prefix with cancel command",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "custom prefix with ok-to-test command",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "multiline comment with custom prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "multiline comment with default prefix only",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
		{
			name: "different custom prefix",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "myteam",
					},
				},
			},
			want: "/myteam ",
		},
		{
			name: "comment with both default and custom prefix prefers custom",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						GitOpsCommandPrefix: "pac",
					},
				},
			},
			want: "/pac ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetGitOpsCommentPrefix(tt.repo)
			assert.Equal(t, tt.want, got)
		})
	}
}
