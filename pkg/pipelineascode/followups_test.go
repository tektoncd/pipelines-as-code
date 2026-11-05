package pipelineascode

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestOnRunDefinitionFor(t *testing.T) {
	defs := map[string]prgroup.Definition{
		"build":  {Name: "build"},
		"tests":  {Name: "tests"},
		"scan":   {Name: "scan"},
		"verify": {Name: "verify", DependsOn: []string{"build", "tests"}},
		"deploy": {Name: "deploy", DependsOn: []string{"verify"}},
		"report": {Name: "report", DependsOn: []string{"scan", "deploy"}},
	}
	tests := []struct {
		name       string
		definition string
		expected   string
	}{
		{
			name:       "a direct dependency of roots is stamped on the smallest root",
			definition: "verify",
			expected:   "build",
		},
		{
			name:       "a chained follow-up bottoms out at the roots of its closure",
			definition: "deploy",
			expected:   "build",
		},
		{
			name:       "the smallest root of the whole closure wins across chains",
			definition: "report",
			expected:   "build",
		},
		{
			name:       "a definition with no dependency is stamped nowhere",
			definition: "build",
			expected:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, onRunDefinitionFor(defs, tt.definition))
		})
	}
}

// matchFor builds a match around a template with the given definition name.
func matchFor(definition string) matcher.Match {
	return matcher.Match{
		PipelineRun: &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: definition + "-",
				Annotations: map[string]string{
					keys.OriginalPRName: definition,
				},
			},
		},
	}
}

func TestBuildFollowUp(t *testing.T) {
	tests := []struct {
		name       string
		definition string
		deps       string
		groupID    string
		wantID     string
		wantDeps   []string
		wantErr    string
	}{
		{
			name:       "the follow-up carries identity, dependencies and state",
			definition: "deploy",
			deps:       `["build", "tests"]`,
			groupID:    "group1",
			wantID:     "deploy-group1",
			wantDeps:   []string{"build", "tests"},
		},
		{
			name:       "a definition whose run name is not a valid kubernetes name fails",
			definition: "Not_Valid",
			deps:       `["build"]`,
			groupID:    "group1",
			wantErr:    "not a valid kubernetes name",
		},
		{
			name:       "a definition whose run name exceeds the PipelineRun name length fails at event time",
			definition: strings.Repeat("d", 60),
			deps:       `["build"]`,
			groupID:    "group1",
			wantErr:    "the definition name must be at most 56 characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := matchFor(tt.definition)
			match.PipelineRun.Annotations[keys.OnPipelineRun] = tt.deps
			event := info.NewEvent()
			event.TriggerTarget = triggertype.Push
			event.EventType = "push"
			event.SHATitle = "merge the feature"
			event.BaseBranch = "refs/heads/main"
			// a GitHub push of a merge commit carries the pull request number
			event.PullRequestNumber = 42
			p := &PacRun{groupID: tt.groupID, event: event, followUpEvent: prgroup.NewFollowUpEvent(event)}

			followUp, err := p.buildFollowUp(match)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantID, followUp.ID)
			assert.Equal(t, tt.definition, followUp.Name)
			assert.DeepEqual(t, tt.wantDeps, followUp.Deps)
			assert.Equal(t, prgroup.StatePending, followUp.State)
			// every follow-up of the event stores the event captured once
			assert.Assert(t, followUp.Event == p.followUpEvent)
		})
	}
}

func TestValidateFollowUpTemplates(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantReports []string
	}{
		{
			name: "a follow-up referencing body is reported",
			raw: `apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
spec:
  params:
    - name: number
      value: "{{ body.pull_request.number }}"`,
			wantReports: []string{"deploy"},
		},
		{
			name: "a follow-up referencing headers is reported",
			raw: `metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
spec:
  params:
    - name: agent
      value: "{{ headers.user-agent }}"`,
			wantReports: []string{"deploy"},
		},
		{
			name: "a follow-up whose cel expression reads the payload is reported",
			raw: `metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
spec:
  params:
    - name: ok
      value: "{{ cel: body.sender == 'me' }}"`,
			wantReports: []string{"deploy"},
		},
		{
			name: "a follow-up using event variables only is clean",
			raw: `metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
spec:
  params:
    - name: revision
      value: "{{ revision }}"
    - name: cel
      value: "{{ cel: pac.sender == 'me' }}"`,
		},
		{
			name: "a plain definition referencing body is not a follow-up concern",
			raw: `metadata:
  name: build
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
spec:
  params:
    - name: number
      value: "{{ body.pull_request.number }}"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validations := validateFollowUpTemplates(tt.raw)
			assert.Equal(t, len(tt.wantReports), len(validations))
			for i, want := range tt.wantReports {
				assert.Equal(t, want, validations[i].Name)
				assert.ErrorContains(t, validations[i].Err, "template variable")
			}
		})
	}
}

func TestStampFollowUps(t *testing.T) {
	deployFollowUp := prgroup.FollowUp{
		ID: "deploy-group1", Name: "deploy",
		Deps: []string{"build"}, State: prgroup.StatePending,
	}
	tests := []struct {
		name        string
		followUps   []prgroup.FollowUp
		followUpsOn map[string]string
		normal      []string
		wantCarried map[string][]string
	}{
		{
			name:        "a follow-up is stamped on the run of its root dependency",
			followUps:   []prgroup.FollowUp{deployFollowUp},
			followUpsOn: map[string]string{"deploy-group1": "build"},
			normal:      []string{"build", "tests"},
			wantCarried: map[string][]string{
				"build": {"deploy-group1"},
				"tests": {},
			},
		},
		{
			name:        "a follow-up whose root dependency did not match is stamped nowhere",
			followUps:   []prgroup.FollowUp{deployFollowUp},
			followUpsOn: map[string]string{"deploy-group1": "scan"},
			normal:      []string{"build"},
			wantCarried: map[string][]string{
				"build": {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PacRun{followUps: tt.followUps, followUpsOn: tt.followUpsOn}
			normal := make([]matcher.Match, 0, len(tt.normal))
			for _, definition := range tt.normal {
				normal = append(normal, matchFor(definition))
			}

			assert.NilError(t, p.stampFollowUps(normal))

			for i := range normal {
				definition := prgroup.DefinitionName(normal[i].PipelineRun)
				followUps, err := prgroup.FollowUpsOf(normal[i].PipelineRun)
				assert.NilError(t, err)
				want := tt.wantCarried[definition]
				assert.Equal(t, len(want), len(followUps),
					"run of %s carries %v, want %v", definition, followUps, want)
				for j, id := range want {
					assert.Equal(t, id, followUps[j].ID)
				}
			}
		})
	}
}

func TestCancelFollowUps(t *testing.T) {
	// two events, each with a build run carrying its own pending follow-up
	newFollowUp := prgroup.FollowUp{
		ID: "deploy-group2", Name: "deploy",
		Deps: []string{"build"}, State: prgroup.StatePending,
	}
	oldFollowUp := prgroup.FollowUp{
		ID: "deploy-group1", Name: "deploy",
		Deps: []string{"build"}, State: prgroup.StatePending,
	}
	newRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "build-new", Namespace: "namespace",
			Labels: map[string]string{keys.GroupID: "group2"},
		},
	}
	oldRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: "build-old", Namespace: "namespace",
			Labels: map[string]string{keys.GroupID: "group1"},
		},
	}
	assert.NilError(t, prgroup.AddFollowUps(newRun, newFollowUp))
	assert.NilError(t, prgroup.AddFollowUps(oldRun, oldFollowUp))

	tests := []struct {
		name    string
		groupID string
		wantNew string
		wantOld string
		// createdRun is a follow-up run the watcher created while the
		// follow-up was being cancelled
		createdRun *tektonv1.PipelineRun
	}{
		{
			// cancel-in-progress: the cancelling event recognizes its own
			// group and leaves its follow-up alone
			name:    "a follow-up of the cancelling event keeps waiting",
			groupID: "group2",
			wantNew: prgroup.StatePending,
			wantOld: prgroup.StateCancelled,
		},
		{
			name:    "a follow-up run created meanwhile is cancelled by its name",
			groupID: "group2",
			wantNew: prgroup.StatePending,
			wantOld: prgroup.StateCancelled,
			createdRun: &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name: "deploy-group1", Namespace: "namespace",
			}},
		},
		{
			// the cancel comment and the closed pull request run before any
			// group is planned: every pending follow-up is consumed
			name:    "without a group of its own the event cancels everything",
			groupID: "",
			wantNew: prgroup.StateCancelled,
			wantOld: prgroup.StateCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			seedNew, seedOld := newRun.DeepCopy(), oldRun.DeepCopy()
			seeds := []*tektonv1.PipelineRun{seedNew, seedOld}
			if tt.createdRun != nil {
				seeds = append(seeds, tt.createdRun.DeepCopy())
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: seeds})
			cs := &params.Run{Clients: clients.Clients{
				Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: zap.NewNop().Sugar(),
			}}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			p := NewPacs(info.NewEvent(), &testprovider.TestProviderImp{},
				cs, &info.PacOpts{}, nil, zap.NewNop().Sugar(), nil)
			p.groupID = tt.groupID

			p.cancelFollowUps(ctx, nil, []tektonv1.PipelineRun{*seedNew, *seedOld}, "a newer event matched")

			for runName, want := range map[string]string{"build-new": tt.wantNew, "build-old": tt.wantOld} {
				fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, runName, metav1.GetOptions{})
				assert.NilError(t, err)
				followUps, err := prgroup.FollowUpsOf(fresh)
				assert.NilError(t, err)
				assert.Equal(t, 1, len(followUps))
				assert.Equal(t, want, followUps[0].State, "follow-up on %s", runName)
			}
			if tt.createdRun != nil {
				fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, tt.createdRun.GetName(), metav1.GetOptions{})
				assert.NilError(t, err)
				assert.Assert(t, fresh.IsGracefullyCancelled(), "%s is not cancelled", fresh.GetName())
			}
		})
	}
}

func TestReportFollowUps(t *testing.T) {
	// deploy waits on build and tests and is stamped on the build run;
	// publish waits on the follow-up tests, itself waiting on build
	deploy := prgroup.FollowUp{
		ID: "deploy-group1", Name: "deploy",
		Deps: []string{"build", "tests"}, State: prgroup.StatePending,
	}
	testsFollowUp := prgroup.FollowUp{
		ID: "tests-group1", Name: "tests",
		Deps: []string{"build"}, State: prgroup.StatePending,
	}
	publish := prgroup.FollowUp{
		ID: "publish-group1", Name: "publish",
		Deps: []string{"tests"}, State: prgroup.StatePending,
	}
	// carrier is the created build run the follow-ups are stamped on
	carrier := func(followUps ...prgroup.FollowUp) *tektonv1.PipelineRun {
		pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
			Name: "build-run", Namespace: "namespace",
			Annotations: map[string]string{keys.OriginalPRName: "build"},
		}}
		assert.NilError(t, prgroup.AddFollowUps(pr, followUps...))
		return pr
	}
	createdRun := func(name, definition string) *tektonv1.PipelineRun {
		return &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "namespace",
			Annotations: map[string]string{keys.OriginalPRName: definition},
		}}
	}

	tests := []struct {
		name        string
		followUps   []prgroup.FollowUp
		createdRuns map[string]*tektonv1.PipelineRun
		// watcherRun is a run the watcher created for a follow-up while the
		// event was still creating its own runs
		watcherRun *tektonv1.PipelineRun
		// wantSkipped holds the definitions reported skipped, in order
		wantSkipped []string
		// wantStates maps a follow-up ID to the state it must carry on the
		// build run after the reporting
		wantStates map[string]string
	}{
		{
			name:        "a follow-up whose dependency did not run is skipped and consumed",
			followUps:   []prgroup.FollowUp{deploy},
			createdRuns: map[string]*tektonv1.PipelineRun{"build": carrier(deploy)},
			wantSkipped: []string{"deploy"},
			wantStates:  map[string]string{deploy.ID: prgroup.StateSkipped},
		},
		{
			name:        "a follow-up the watcher already created is consumed as created, not skipped",
			followUps:   []prgroup.FollowUp{deploy},
			createdRuns: map[string]*tektonv1.PipelineRun{"build": carrier(deploy)},
			watcherRun:  createdRun(deploy.ID, "deploy"),
			wantSkipped: nil,
			wantStates:  map[string]string{deploy.ID: prgroup.StateCreated},
		},
		{
			name:      "a follow-up whose every dependency ran waits silently",
			followUps: []prgroup.FollowUp{deploy},
			createdRuns: map[string]*tektonv1.PipelineRun{
				"build": carrier(deploy),
				"tests": createdRun("tests-run", "tests"),
			},
			wantSkipped: nil,
			wantStates:  map[string]string{deploy.ID: prgroup.StatePending},
		},
		{
			// only chained dependencies, whose runs never exist at event time:
			// the build run the follow-ups were to be stamped on did not run
			name:        "a follow-up never stamped anywhere is skipped",
			followUps:   []prgroup.FollowUp{testsFollowUp, publish},
			createdRuns: map[string]*tektonv1.PipelineRun{},
			wantSkipped: []string{"tests", "publish"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			seeds := make([]*tektonv1.PipelineRun, 0, len(tt.createdRuns)+1)
			for _, pr := range tt.createdRuns {
				seeds = append(seeds, pr)
			}
			if tt.watcherRun != nil {
				seeds = append(seeds, tt.watcherRun)
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: seeds})
			cs := &params.Run{Clients: clients.Clients{
				Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: zap.NewNop().Sugar(),
			}}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			vcx := &statusRecordingProvider{}
			p := NewPacs(info.NewEvent(), vcx, cs, &info.PacOpts{}, nil, zap.NewNop().Sugar(), nil)
			p.followUps = tt.followUps
			for _, followUp := range tt.followUps {
				p.followUpsOn[followUp.ID] = "build"
			}
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"}}

			p.reportFollowUps(ctx, repo, tt.createdRuns)

			skipped := make([]string, 0, len(vcx.statuses))
			for _, status := range vcx.statuses {
				assert.Equal(t, providerstatus.ConclusionSkipped, status.Conclusion)
				skipped = append(skipped, status.OriginalPipelineRunName)
			}
			assert.DeepEqual(t, append([]string{}, tt.wantSkipped...), skipped)
			if len(tt.wantStates) == 0 {
				return
			}
			fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, "build-run", metav1.GetOptions{})
			assert.NilError(t, err)
			followUps, err := prgroup.FollowUpsOf(fresh)
			assert.NilError(t, err)
			states := map[string]string{}
			for _, followUp := range followUps {
				states[followUp.ID] = followUp.State
			}
			assert.DeepEqual(t, tt.wantStates, states)
		})
	}
}

// statusRecordingProvider records the checks posted for the follow-ups.
type statusRecordingProvider struct {
	testprovider.TestProviderImp
	statuses []providerstatus.StatusOpts
	shas     []string
}

func (v *statusRecordingProvider) CreateStatus(_ context.Context, event *info.Event, opts providerstatus.StatusOpts) error {
	v.statuses = append(v.statuses, opts)
	v.shas = append(v.shas, event.SHA)
	return nil
}

func TestPlanFollowUps(t *testing.T) {
	definition := func(name, deps string) *tektonv1.PipelineRun {
		pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{keys.OriginalPRName: name},
		}}
		if deps != "" {
			pr.Annotations[keys.OnPipelineRun] = deps
			pr.Annotations[keys.OnCelExpression] = "true"
		}
		return pr
	}
	build := definition("build", "")
	tests := definition("tests", "")
	deploy := definition("deploy", `["build", "tests"]`)
	publish := definition("publish", `["deploy"]`)
	lint := definition("lint", "")
	allDefs := []*tektonv1.PipelineRun{build, tests, deploy, publish, lint}
	match := func(pr *tektonv1.PipelineRun) matcher.Match {
		m := matcher.Match{PipelineRun: pr, Config: map[string]string{}}
		if _, ok := pr.Annotations[keys.OnPipelineRun]; ok {
			m.Config[matcher.ConfigFollowUp] = "true"
		}
		return m
	}

	cases := []struct {
		name          string
		eventType     string
		matched       []*tektonv1.PipelineRun
		unmatched     []*tektonv1.PipelineRun
		wantKept      []string
		wantFollowUps []string
		wantSkipped   []string
		wantUnmatched []string
		// targetRepos sets the Repository a matched definition resolved to
		// through its target-namespace annotation
		targetRepos map[string]*v1alpha1.Repository
		// payload is the webhook payload of the event
		payload string
		// disabled leaves the follow-ups feature off in the ConfigMap
		disabled bool
	}{
		{
			name:      "a retest after a partial success re-runs the failed definition only",
			eventType: opscomments.RetestAllCommentEventType.String(),
			// build already succeeded: the matcher dropped it from both lists
			matched:  []*tektonv1.PipelineRun{tests, deploy, publish},
			wantKept: []string{"tests"},
		},
		{
			name:          "a retest re-running every dependency re-creates the follow-ups",
			eventType:     opscomments.RetestAllCommentEventType.String(),
			matched:       []*tektonv1.PipelineRun{build, tests, deploy, publish},
			wantKept:      []string{"build", "tests"},
			wantFollowUps: []string{"deploy", "publish"},
		},
		{
			name:          "a dependency not matching the event still skips its follow-ups with a check",
			eventType:     "pull_request",
			matched:       []*tektonv1.PipelineRun{tests, deploy, publish},
			unmatched:     []*tektonv1.PipelineRun{build},
			wantKept:      []string{"tests"},
			wantSkipped:   []string{"deploy", "publish"},
			wantUnmatched: []string{"build"},
		},
		{
			name:          "a follow-up none of whose dependencies run is unmatched without a check",
			eventType:     "pull_request",
			matched:       []*tektonv1.PipelineRun{lint, deploy, publish},
			unmatched:     []*tektonv1.PipelineRun{build, tests},
			wantKept:      []string{"lint"},
			wantUnmatched: []string{"build", "deploy", "publish", "tests"},
		},
		{
			name:      "a follow-up targeting another Repository than its dependencies rejects the event",
			eventType: "pull_request",
			matched:   []*tektonv1.PipelineRun{build, tests, deploy, publish},
			targetRepos: map[string]*v1alpha1.Repository{
				"deploy": {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
			},
		},
		{
			name:      "a dependency targeting another Repository than its siblings' follow-up rejects the event",
			eventType: "pull_request",
			matched:   []*tektonv1.PipelineRun{build, tests, deploy, publish},
			targetRepos: map[string]*v1alpha1.Repository{
				"tests": {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
			},
		},
		{
			name:      "a group targeting one other Repository as a whole is planned",
			eventType: "pull_request",
			matched:   []*tektonv1.PipelineRun{build, tests, deploy, publish},
			targetRepos: map[string]*v1alpha1.Repository{
				"build":   {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
				"tests":   {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
				"deploy":  {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
				"publish": {ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "prod"}},
			},
			wantKept:      []string{"build", "tests"},
			wantFollowUps: []string{"deploy", "publish"},
		},
		{
			name:      "an incoming webhook overriding params rejects the event when follow-ups form",
			eventType: "incoming",
			matched:   []*tektonv1.PipelineRun{build, tests, deploy, publish},
			payload:   `{"params": {"environment": "prod"}}`,
		},
		{
			name:          "an incoming webhook without params plans the follow-ups",
			eventType:     "incoming",
			matched:       []*tektonv1.PipelineRun{build, tests, deploy, publish},
			payload:       `{"repository": "repo", "branch": "main"}`,
			wantKept:      []string{"build", "tests"},
			wantFollowUps: []string{"deploy", "publish"},
		},
		{
			name:      "an incoming webhook overriding params runs when no follow-up forms",
			eventType: "incoming",
			matched:   []*tektonv1.PipelineRun{lint},
			payload:   `{"params": {"environment": "prod"}}`,
			wantKept:  []string{"lint"},
		},
		{
			name:      "the feature disabled in the ConfigMap drops the on-pipelinerun definitions",
			eventType: "pull_request",
			matched:   []*tektonv1.PipelineRun{build, tests, deploy, publish},
			disabled:  true,
			// they cannot run as plain runs: their CEL condition is never
			// evaluated, they would run before their dependencies
			wantKept: []string{"build", "tests"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			cs := &params.Run{Clients: clients.Clients{
				Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: zap.NewNop().Sugar(),
			}}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			event := info.NewEvent()
			event.EventType = tt.eventType
			event.Request.Payload = []byte(tt.payload)
			vcx := &statusRecordingProvider{}
			pacOpts := info.NewPacOpts()
			pacOpts.EnableFollowUpRuns = !tt.disabled
			p := NewPacs(event, vcx, cs, pacOpts, nil, zap.NewNop().Sugar(), nil)

			matched := make([]matcher.Match, 0, len(tt.matched))
			for _, pr := range tt.matched {
				m := match(pr)
				m.Repo = tt.targetRepos[pr.GetName()]
				matched = append(matched, m)
			}
			unmatched := map[string]*tektonv1.PipelineRun{}
			for _, pr := range tt.unmatched {
				unmatched[pr.GetName()] = pr
			}
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"}}

			kept := p.planFollowUps(ctx, repo, matched, unmatched, allDefs)

			keptNames := make([]string, 0, len(kept))
			for _, m := range kept {
				keptNames = append(keptNames, prgroup.DefinitionName(m.PipelineRun))
			}
			assert.DeepEqual(t, append([]string{}, tt.wantKept...), keptNames)
			followUpNames := make([]string, 0, len(p.followUps))
			for _, fu := range p.followUps {
				followUpNames = append(followUpNames, fu.Name)
			}
			assert.DeepEqual(t, append([]string{}, tt.wantFollowUps...), followUpNames)
			skipped := make([]string, 0, len(vcx.statuses))
			for _, status := range vcx.statuses {
				assert.Equal(t, providerstatus.ConclusionSkipped, status.Conclusion)
				skipped = append(skipped, status.OriginalPipelineRunName)
			}
			assert.DeepEqual(t, append([]string{}, tt.wantSkipped...), skipped)
			unmatchedNames := make([]string, 0, len(unmatched))
			for name := range unmatched {
				unmatchedNames = append(unmatchedNames, name)
			}
			slices.Sort(unmatchedNames)
			assert.DeepEqual(t, append([]string{}, tt.wantUnmatched...), unmatchedNames)
		})
	}
}

func TestCreationPhases(t *testing.T) {
	match := func(name string, carrier bool) matcher.Match {
		pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: map[string]string{}}}
		if carrier {
			pr.Annotations[keys.FollowUps] = "[]"
		}
		return matcher.Match{PipelineRun: pr}
	}
	tests := []struct {
		name    string
		matches []matcher.Match
		want    [][]int
	}{
		{
			name:    "the runs carrying follow-ups are created after the others",
			matches: []matcher.Match{match("build", true), match("tests", false), match("lint", false)},
			want:    [][]int{{1, 2}, {0}},
		},
		{
			// build carries deploy waiting on build and tests, tests carries
			// notify: tests must exist before build can finish
			name:    "the runs carrying follow-ups are created one at a time by descending name",
			matches: []matcher.Match{match("build", true), match("lint", false), match("tests", true), match("audit", true)},
			want:    [][]int{{1}, {2}, {0}, {3}},
		},
		{
			name:    "without follow-ups every run is created in the first phase",
			matches: []matcher.Match{match("build", false), match("tests", false)},
			want:    [][]int{{0, 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.DeepEqual(t, tt.want, creationPhases(tt.matches))
		})
	}
}

func TestDependsOnAny(t *testing.T) {
	defsByName := map[string]prgroup.Definition{
		"build":   {Name: "build"},
		"deploy":  {Name: "deploy", DependsOn: []string{"build"}},
		"publish": {Name: "publish", DependsOn: []string{"deploy"}},
	}
	cases := []struct {
		name    string
		def     string
		targets map[string]bool
		want    bool
	}{
		{name: "direct dependency", def: "deploy", targets: map[string]bool{"build": true}, want: true},
		{name: "dependency through a chain", def: "publish", targets: map[string]bool{"build": true}, want: true},
		{name: "unrelated target", def: "publish", targets: map[string]bool{"tests": true}},
		{name: "no target", def: "publish"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, dependsOnAny(defsByName, tt.def, tt.targets))
		})
	}
}

func TestCancelInProgressFollowUpScope(t *testing.T) {
	// run is a run of the given definition in a group, on the main branch
	// unless told otherwise, carrying a follow-up in the given state
	run := func(name, definition, deps, branch, sha, groupID, followUpState string, done bool) *tektonv1.PipelineRun {
		pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "namespace",
			Labels: map[string]string{
				keys.URLRepository:  "repo",
				keys.OriginalPRName: definition,
				keys.GroupID:        groupID,
			},
			Annotations: map[string]string{
				keys.OriginalPRName:   definition,
				keys.SourceBranch:     branch,
				keys.SHA:              sha,
				keys.GroupID:          groupID,
				keys.CancelInProgress: "true",
			},
		}}
		if deps != "" {
			pr.Annotations[keys.OnPipelineRun] = deps
		}
		if done {
			pr.Status.SetCondition(&apis.Condition{Type: apis.ConditionSucceeded, Status: corev1.ConditionTrue})
		}
		if followUpState != "" {
			assert.NilError(t, prgroup.AddFollowUps(pr, prgroup.FollowUp{
				ID: "deploy-" + groupID, Name: "deploy",
				Deps: []string{definition}, State: followUpState,
			}))
		}
		return pr
	}
	carrier := func(name, branch, sha, groupID string) *tektonv1.PipelineRun {
		return run(name, "build", "", branch, sha, groupID, prgroup.StatePending, false)
	}

	tests := []struct {
		name          string
		runs          []*tektonv1.PipelineRun
		wantState     map[string]string
		wantCancelled []string
		wantRunning   []string
		wantSHAs      []string
	}{
		{
			name: "a push only cancels the follow-ups of the superseded run of its branch",
			runs: []*tektonv1.PipelineRun{
				carrier("build-main-new", "main", "sha-new", "group-new"),
				carrier("build-main-old", "main", "sha-old", "group-old"),
				carrier("build-feature", "feature", "sha-feature", "group-feature"),
			},
			wantState: map[string]string{
				"build-main-new": prgroup.StatePending,
				"build-main-old": prgroup.StateCancelled,
				"build-feature":  prgroup.StatePending,
			},
			wantCancelled: []string{"build-main-old"},
			wantRunning:   []string{"build-main-new", "build-feature"},
			// the cancelled check lands on the superseded commit
			wantSHAs: []string{"sha-old"},
		},
		{
			name: "the follow-up runs after a finished superseded run are cancelled with what waits on them",
			runs: []*tektonv1.PipelineRun{
				carrier("build-main-new", "main", "sha-new", "group-new"),
				// the superseded build finished and created its follow-up
				run("build-main-old", "build", "", "main", "sha-old", "group-old", prgroup.StateCreated, true),
				// tests follows build and still runs, deploy waits on it
				run("tests-old", "tests", `["build"]`, "main", "sha-old", "group-old", prgroup.StatePending, false),
				// notify follows lint only: not downstream of the superseded build
				run("notify-old", "notify", `["lint"]`, "main", "sha-old", "group-old", "", false),
			},
			wantState: map[string]string{
				"build-main-new": prgroup.StatePending,
				"build-main-old": prgroup.StateCreated,
				"tests-old":      prgroup.StateCancelled,
			},
			wantCancelled: []string{"tests-old"},
			wantRunning:   []string{"build-main-new", "notify-old"},
			wantSHAs:      []string{"sha-old"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: tt.runs})
			cs := &params.Run{Clients: clients.Clients{
				Tekton: stdata.Pipeline, Kube: stdata.Kube, Log: zap.NewNop().Sugar(),
			}}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			event := info.NewEvent()
			event.TriggerTarget = triggertype.Push
			event.Repository = "repo"
			event.HeadBranch = "main"
			event.SHA = "sha-new"
			vcx := &statusRecordingProvider{}
			p := NewPacs(event, vcx, cs, &info.PacOpts{}, nil, zap.NewNop().Sugar(), nil)
			p.groupID = "group-new"
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"}}

			assert.NilError(t, p.cancelInProgressMatchingPipelineRun(ctx, tt.runs[0], repo))

			for runName, want := range tt.wantState {
				fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, runName, metav1.GetOptions{})
				assert.NilError(t, err)
				followUps, err := prgroup.FollowUpsOf(fresh)
				assert.NilError(t, err)
				assert.Equal(t, want, followUps[0].State, "follow-up on %s", runName)
			}
			for _, runName := range tt.wantCancelled {
				fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, runName, metav1.GetOptions{})
				assert.NilError(t, err)
				assert.Assert(t, fresh.IsCancelled() || fresh.IsGracefullyCancelled(), "%s is not cancelled", runName)
			}
			for _, runName := range tt.wantRunning {
				fresh, err := stdata.Pipeline.TektonV1().PipelineRuns("namespace").Get(ctx, runName, metav1.GetOptions{})
				assert.NilError(t, err)
				assert.Equal(t, tektonv1.PipelineRunSpecStatus(""), fresh.Spec.Status, "%s is cancelled", runName)
			}
			assert.DeepEqual(t, tt.wantSHAs, vcx.shas)
		})
	}
}

func TestEventForRun(t *testing.T) {
	event := info.NewEvent()
	event.SHA = "sha-new"
	tests := []struct {
		name    string
		runSHA  string
		wantSHA string
		wantNew bool
	}{
		{name: "run of another commit", runSHA: "sha-old", wantSHA: "sha-old", wantNew: true},
		{name: "run of the same commit", runSHA: "sha-new", wantSHA: "sha-new"},
		{name: "run without a commit annotation", wantSHA: "sha-new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
			if tt.runSHA != "" {
				run.Annotations[keys.SHA] = tt.runSHA
			}
			got := eventForRun(event, run)
			assert.Equal(t, tt.wantSHA, got.SHA)
			assert.Equal(t, tt.wantNew, got != event)
			assert.Equal(t, "sha-new", event.SHA, "the current event must not be mutated")
		})
	}
}

func TestCaptureFollowUpEvent(t *testing.T) {
	repoParams := []v1alpha1.Params{
		// a filter reading the payload, gone when the follow-up is templated
		{Name: "stage", Value: "review", Filter: `body.action == "opened"`},
		{Name: "stage", Value: "ship", Filter: `pac.event_type == "push"`},
		{Name: "token", SecretRef: &v1alpha1.Secret{Name: "creds", Key: "token"}},
		{Name: "region", Value: "eu"},
	}
	tests := []struct {
		name        string
		templated   bool
		wantFilters map[string]string
		wantTitle   string
	}{
		{
			name:        "the templated event and the filter outcomes are captured",
			templated:   true,
			wantFilters: map[string]string{"stage": `body.action == "opened"`},
			wantTitle:   "templated title",
		},
		{
			name:        "without a templated event the current one is captured",
			wantFilters: map[string]string{"stage": `body.action == "opened"`},
			wantTitle:   "current title",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			cs := &params.Run{Clients: clients.Clients{Kube: stdata.Kube, Tekton: stdata.Pipeline, Log: zap.NewNop().Sugar()}}
			event := info.NewEvent()
			event.TriggerTarget = triggertype.PullRequest
			event.EventType = "pull_request"
			event.PullRequestTitle = "current title"
			event.Event = map[string]any{"action": "opened"}
			p := NewPacs(event, &testprovider.TestProviderImp{}, cs, &info.PacOpts{}, nil, zap.NewNop().Sugar(), nil)
			if tt.templated {
				templated := *event
				templated.PullRequestTitle = "templated title"
				p.templatedEvent = &templated
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"},
				Spec:       v1alpha1.RepositorySpec{Params: &repoParams},
			}

			captured := p.captureFollowUpEvent(ctx, repo)
			assert.Equal(t, tt.wantTitle, captured.PullRequestTitle)
			// only the filters, never a param value
			assert.DeepEqual(t, tt.wantFilters, captured.ParamFilters)
			// the repository the definition was templated from, for the
			// watcher to template the follow-up from the same source
			assert.Equal(t, "namespace", captured.SourceRepoNamespace)
			assert.Equal(t, "repo", captured.SourceRepoName)
		})
	}
}
