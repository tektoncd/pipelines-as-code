package resolve

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	assertfs "gotest.tools/v3/fs"
	"gotest.tools/v3/golden"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var tmplSimpleNoPrefix = `
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: test
spec:
  pipelineSpec:
	tasks:
	  - name: {{foo}}
		taskSpec:
		  steps:
			- name: hello-moto
			  image: alpine:3.7
			  script: "echo hello moto"`

func TestSplitArgsInMap(t *testing.T) {
	args := []string{"ride=bike", "be=free", "of=car"}
	ret := splitArgsInMap(args)

	if _, ok := ret["ride"]; !ok {
		t.Error("args hasn't been split")
	}
}

func TestCommandFilenameSetProperly(t *testing.T) {
	tdata := testclient.Data{}
	ctx, _ := rtesting.SetupFakeContext(t)
	//nolint
	io, _, _, _ := cli.IOTest()
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	cs := &params.Run{
		Clients: clients.Clients{
			Kube:              stdata.Kube,
			ClientInitialized: true,
		},
		Info: info.Info{Pac: &info.PacOpts{Settings: settings.Settings{}}},
	}
	cmd := Command(cs, io)
	e := bytes.NewBufferString("")
	o := bytes.NewBufferString("")
	cmd.SetErr(e)
	cmd.SetOut(o)
	cmd.SetArgs([]string{""})
	err := cmd.Execute()
	assert.ErrorContains(t, err, "you need to at least specify a file")
}

func TestResolveFilenames(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	cs := &params.Run{Clients: clients.Clients{Log: fakelogger}}

	tmplSimpleWithPrefix := fmt.Sprintf("---\n%s", tmplSimpleNoPrefix)

	tests := []struct {
		name      string
		tmpl      string
		wantErr   bool
		asv1beta1 bool
	}{
		{
			name:    "Resolve templates no prefix",
			tmpl:    tmplSimpleNoPrefix,
			wantErr: false,
		},
		{
			name:      "Resolve templates no prefix as v1beta1",
			tmpl:      tmplSimpleNoPrefix,
			wantErr:   false,
			asv1beta1: true,
		},
		{
			name:    "Resolve templates with prefix",
			tmpl:    tmplSimpleWithPrefix,
			wantErr: false,
		},
		{
			name:    "No pipelinerun",
			tmpl:    `---\nfoo:bar`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := assertfs.NewDir(t, "test-name",
				assertfs.WithFile("file.yaml", strings.ReplaceAll(tt.tmpl, "\t", "    ")))
			defer dir.Remove()
			ctx, _ := rtesting.SetupFakeContext(t)
			got, err := resolveFilenames(ctx, cs, io.Discard, []string{dir.Path()}, map[string]string{"foo": "bar"}, tt.asv1beta1)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveFilenames() error = %v, wantErr %v", err, tt.wantErr)
				return
			} else if !tt.wantErr {
				golden.Assert(t, got, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
				assert.Assert(t, got != "")
			}
		})
	}
}
