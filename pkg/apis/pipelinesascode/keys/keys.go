/*
CoCopyright 2022 Red Hat

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package keys

import (
	"regexp"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
)

const (
	ControllerInfo         = pipelinesascode.GroupName + "/controller-info"
	Task                   = pipelinesascode.GroupName + "/task"
	Pipeline               = pipelinesascode.GroupName + "/pipeline"
	URLOrg                 = pipelinesascode.GroupName + "/url-org"
	URLRepository          = pipelinesascode.GroupName + "/url-repository"
	SHA                    = pipelinesascode.GroupName + "/sha"
	Sender                 = pipelinesascode.GroupName + "/sender"
	EventType              = pipelinesascode.GroupName + "/event-type"
	Branch                 = pipelinesascode.GroupName + "/branch"
	SourceBranch           = pipelinesascode.GroupName + "/source-branch"
	Repository             = pipelinesascode.GroupName + "/repository"
	GitProvider            = pipelinesascode.GroupName + "/git-provider"
	State                  = pipelinesascode.GroupName + "/state"
	ShaTitle               = pipelinesascode.GroupName + "/sha-title"
	ShaURL                 = pipelinesascode.GroupName + "/sha-url"
	RepoURL                = pipelinesascode.GroupName + "/repo-url"
	SourceRepoURL          = pipelinesascode.GroupName + "/source-repo-url"
	PullRequest            = pipelinesascode.GroupName + "/pull-request"
	InstallationID         = pipelinesascode.GroupName + "/installation-id"
	GHEURL                 = pipelinesascode.GroupName + "/ghe-url"
	SourceProjectID        = pipelinesascode.GroupName + "/source-project-id"
	TargetProjectID        = pipelinesascode.GroupName + "/target-project-id"
	OriginalPRName         = pipelinesascode.GroupName + "/original-prname"
	GitAuthSecret          = pipelinesascode.GroupName + "/git-auth-secret"
	CheckRunID             = pipelinesascode.GroupName + "/check-run-id"
	OnEvent                = pipelinesascode.GroupName + "/on-event"
	OnComment              = pipelinesascode.GroupName + "/on-comment"
	OnTargetBranch         = pipelinesascode.GroupName + "/on-target-branch"
	OnPathChange           = pipelinesascode.GroupName + "/on-path-change"
	OnLabel                = pipelinesascode.GroupName + "/on-label"
	OnPathChangeIgnore     = pipelinesascode.GroupName + "/on-path-change-ignore"
	OnCelExpression        = pipelinesascode.GroupName + "/on-cel-expression"
	TargetNamespace        = pipelinesascode.GroupName + "/target-namespace"
	MaxKeepRuns            = pipelinesascode.GroupName + "/max-keep-runs"
	CancelInProgress       = pipelinesascode.GroupName + "/cancel-in-progress"
	LogURL                 = pipelinesascode.GroupName + "/log-url"
	ExecutionOrder         = pipelinesascode.GroupName + "/execution-order"
	SCMReportingPLRStarted = pipelinesascode.GroupName + "/scm-reporting-plr-started"
	SecretCreated          = pipelinesascode.GroupName + "/secret-created"
	CloneURL               = pipelinesascode.GroupName + "/clone-url"
	// PublicGithubAPIURL default is "https://api.github.com" but it can be overridden by X-GitHub-Enterprise-Host header.
	PublicGithubAPIURL   = "https://api.github.com"
	GithubApplicationID  = "github-application-id"
	GithubPrivateKey     = "github-private-key"
	ResultsRecordSummary = "results.tekton.dev/recordSummaryAnnotations"

	// SpanContextAnnotation is the annotation key for propagating span context to Tekton for distributed tracing.
	SpanContextAnnotation = "tekton.dev/pipelinerunSpanContext"
)

var ParamsRe = regexp.MustCompile(`{{([^}]{2,})}}`)
