package sort

import (
	"regexp"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStatusTmpl(t *testing.T) {
	flattedTmpl := `{{- range $taskrun := .TaskRunList }}{{- $taskrun.ConsoleLogURL }}{{- end }}`

	tests := []struct {
		name            string
		wantErr         bool
		prTaskRunStatus map[string]*tektonv1.PipelineRunTaskRunStatus
		tmpl            string
		wantRegexp      *regexp.Regexp
	}{
		{
			name:    "badtemplate",
			wantErr: true,
			tmpl:    "{{.XXX}}",
			prTaskRunStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"first":  tektontest.MakePrTrStatus("first", "", 5),
				"last":   tektontest.MakePrTrStatus("last", "", 15),
				"middle": tektontest.MakePrTrStatus("middle", "", 10),
			},
		},
		{
			name:       "sorted",
			wantRegexp: regexp.MustCompile(".*first.*middle.*last.*"),
			tmpl:       flattedTmpl,
			prTaskRunStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"first":  tektontest.MakePrTrStatus("first", "", 5),
				"last":   tektontest.MakePrTrStatus("last", "", 15),
				"middle": tektontest.MakePrTrStatus("middle", "", 10),
			},
		},
		{
			name:       "sorted with displayname",
			wantRegexp: regexp.MustCompile(".*Primo.*Median.*Ultimo"),
			tmpl:       flattedTmpl,
			prTaskRunStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"first":  tektontest.MakePrTrStatus("first", "Primo", 5),
				"last":   tektontest.MakePrTrStatus("last", "Ultimo", 15),
				"middle": tektontest.MakePrTrStatus("middle", "Median", 10),
			},
		},
		{
			name:       "not completed yet come first",
			wantRegexp: regexp.MustCompile(".*notcompleted.*first.*last.*"),
			tmpl:       flattedTmpl,
			prTaskRunStatus: map[string]*tektonv1.PipelineRunTaskRunStatus{
				"first":  tektontest.MakePrTrStatus("first", "", 5),
				"last":   tektontest.MakePrTrStatus("last", "", 15),
				"middle": tektontest.MakePrTrStatus("notcompleted", "", -1),
			},
		},
		{
			name:            "test sorted status nada",
			wantRegexp:      regexp.MustCompile("PipelineRun has no taskruns"),
			prTaskRunStatus: make(map[string]*tektonv1.PipelineRunTaskRunStatus),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := params.New()
			runs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			config := &info.ProviderConfig{
				TaskStatusTMPL: tt.tmpl,
			}
			pr := &tektonv1.PipelineRun{}
			output, err := TaskStatusTmpl(pr, tt.prTaskRunStatus, runs, info.NewPacOpts(), config)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			if tt.wantRegexp != nil {
				assert.Assert(t, tt.wantRegexp.MatchString(output), "%s != %s", output, tt.wantRegexp.String())
			}
		})
	}
}

func TestStatusTmplSameStartTime(t *testing.T) {
	flattedTmpl := `{{- range $taskrun := .TaskRunList }}{{- $taskrun.ConsoleLogURL }}{{- end }}`
	wantRegexp := regexp.MustCompile(".*alpha.*zebra.*")

	// Use a single shared time to ensure Equal() returns true
	sharedTime := &metav1.Time{Time: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)}

	prTaskRunStatus := map[string]*tektonv1.PipelineRunTaskRunStatus{
		"zebra": {
			PipelineTaskName: "zebra",
			Status: &tektonv1.TaskRunStatus{
				TaskRunStatusFields: tektonv1.TaskRunStatusFields{
					StartTime: sharedTime,
				},
			},
		},
		"alpha": {
			PipelineTaskName: "alpha",
			Status: &tektonv1.TaskRunStatus{
				TaskRunStatusFields: tektonv1.TaskRunStatusFields{
					StartTime: sharedTime,
				},
			},
		},
	}

	config := &info.ProviderConfig{
		TaskStatusTMPL: flattedTmpl,
	}
	runs := params.New()
	runs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	pr := &tektonv1.PipelineRun{}
	output, err := TaskStatusTmpl(pr, prTaskRunStatus, runs, info.NewPacOpts(), config)
	assert.NilError(t, err)
	assert.Assert(t, wantRegexp.MatchString(output), "%s != %s", output, wantRegexp.String())
}

func TestConsoleLogHTMLLink(t *testing.T) {
	tests := []struct {
		name string
		task tkr
		want string
	}{
		{
			name: "task name",
			task: tkr{
				taskLogURL:               "https://console/task",
				PipelineRunTaskRunStatus: &tektonv1.PipelineRunTaskRunStatus{PipelineTaskName: "build"},
			},
			want: `<a href="https://console/task">build</a>`,
		},
		{
			name: "display name of the task",
			task: tkr{
				taskLogURL: "https://console/task",
				PipelineRunTaskRunStatus: &tektonv1.PipelineRunTaskRunStatus{
					PipelineTaskName: "build",
					Status: &tektonv1.TaskRunStatus{
						TaskRunStatusFields: tektonv1.TaskRunStatusFields{
							TaskSpec: &tektonv1.TaskSpec{DisplayName: "Build the thing"},
						},
					},
				},
			},
			want: `<a href="https://console/task">Build the thing</a>`,
		},
		{
			name: "html in the name and the url is escaped",
			task: tkr{
				taskLogURL:               `https://console/task?a=1&b=2`,
				PipelineRunTaskRunStatus: &tektonv1.PipelineRunTaskRunStatus{PipelineTaskName: `<script>`},
			},
			want: `<a href="https://console/task?a=1&amp;b=2">&lt;script&gt;</a>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.task.ConsoleLogHTMLLink(), tt.want)
		})
	}
}
