package opscomments

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"go.uber.org/zap"
)

type EventType string

func (e EventType) String() string {
	return string(e)
}

var (
	NoOpsCommentEventType        = EventType("no-ops-comment")
	TestAllCommentEventType      = EventType("test-all-comment")
	TestSingleCommentEventType   = EventType("test-comment")
	RetestSingleCommentEventType = EventType("retest-comment")
	RetestAllCommentEventType    = EventType("retest-all-comment")
	OnCommentEventType           = EventType("on-comment")
	CancelCommentSingleEventType = EventType("cancel-comment")
	CancelCommentAllEventType    = EventType("cancel-all-comment")
	OkToTestCommentEventType     = EventType("ok-to-test-comment")
)

func RetestAllRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%sretest\s*$`, prefix))
}

func RetestSingleRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%sretest[ \t]+\S+`, prefix))
}

func TestAllRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%stest\s*$`, prefix))
}

func TestSingleRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%stest[ \t]+\S+`, prefix))
}

func OkToTestRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(^|\n)\s*%sok-to-test(?:\s+([a-fA-F0-9]{7,40}))?\s*(\r\n|\r|\n|$)`, prefix))
}

func CancelAllRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%scancel\s*$`, prefix))
}

func CancelSingleRegex(prefix string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%scancel[ \t]+\S+`, prefix))
}

func CommentEventType(comment, prefix string) EventType {
	switch {
	case RetestAllRegex(prefix).MatchString(comment):
		return RetestAllCommentEventType
	case RetestSingleRegex(prefix).MatchString(comment):
		return RetestSingleCommentEventType
	case TestAllRegex(prefix).MatchString(comment):
		return TestAllCommentEventType
	case TestSingleRegex(prefix).MatchString(comment):
		return TestSingleCommentEventType
	case OkToTestRegex(prefix).MatchString(comment):
		return OkToTestCommentEventType
	case CancelAllRegex(prefix).MatchString(comment):
		return CancelCommentAllEventType
	case CancelSingleRegex(prefix).MatchString(comment):
		return CancelCommentSingleEventType
	default:
		return NoOpsCommentEventType
	}
}

// SetEventTypeAndTargetPR function will set the event type and target test pipeline run in an event.
func SetEventTypeAndTargetPR(event *info.Event, comment, prefix string) {
	commentType := CommentEventType(comment, prefix)
	if commentType == RetestSingleCommentEventType {
		typeOfComment := prefix + "retest"
		event.TargetTestPipelineRun = getNameFromComment(typeOfComment, comment)
	}
	if commentType == TestSingleCommentEventType {
		typeOfComment := prefix + "test"
		event.TargetTestPipelineRun = getNameFromComment(typeOfComment, comment)
	}
	if commentType == CancelCommentAllEventType || commentType == CancelCommentSingleEventType {
		event.CancelPipelineRuns = true
	}
	if commentType == CancelCommentSingleEventType {
		typeOfComment := prefix + "cancel"
		event.TargetCancelPipelineRun = getNameFromComment(typeOfComment, comment)
	}
	event.EventType = commentType.String()
	event.TriggerComment = comment
}

func IsOkToTestComment(comment, prefix string) bool {
	return OkToTestRegex(prefix).MatchString(comment)
}

func IsTestRetestComment(comment, prefix string) bool {
	return TestSingleRegex(prefix).MatchString(comment) || TestAllRegex(prefix).MatchString(comment) ||
		RetestSingleRegex(prefix).MatchString(comment) || RetestAllRegex(prefix).MatchString(comment)
}

func IsCancelComment(comment, prefix string) bool {
	return CancelAllRegex(prefix).MatchString(comment) || CancelSingleRegex(prefix).MatchString(comment)
}

// GetSHAFromOkToTestComment extracts the optional SHA from an /ok-to-test comment.
func GetSHAFromOkToTestComment(comment, prefix string) string {
	matches := OkToTestRegex(prefix).FindStringSubmatch(comment)
	if len(matches) > 2 {
		return strings.TrimSpace(matches[2])
	}
	return ""
}

// EventTypeBackwardCompat handle the backward compatibility we need to keep until
// we have done the deprecated notice
//
// 2024-07-01 chmouel
//
//	set anyOpsComments to pull_request see https://issues.redhat.com/browse/SRVKP-5775
//	we keep on-comment to the "on-comment" type
func EventTypeBackwardCompat(eventEmitter *events.EventEmitter, repo *v1alpha1.Repository, label string) string {
	if label == OnCommentEventType.String() {
		return label
	}
	if IsAnyOpsEventType(label) {
		eventEmitter.EmitMessage(repo, zap.WarnLevel, "DeprecatedOpsComment",
			fmt.Sprintf("the %s event type is deprecated, this will be changed to %s in the future",
				label, triggertype.PullRequest.String()))
		return triggertype.PullRequest.String()
	}
	return label
}

func IsAnyOpsEventType(eventType string) bool {
	return eventType == TestSingleCommentEventType.String() ||
		eventType == TestAllCommentEventType.String() ||
		eventType == RetestAllCommentEventType.String() ||
		eventType == RetestSingleCommentEventType.String() ||
		eventType == CancelCommentSingleEventType.String() ||
		eventType == CancelCommentAllEventType.String() ||
		eventType == OkToTestCommentEventType.String() ||
		eventType == OnCommentEventType.String()
}

// AnyOpsKubeLabelInSelector will output a Kubernetes label out of all possible
// CommentEvent Type for selection.
func AnyOpsKubeLabelInSelector() string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s",
		TestSingleCommentEventType.String(),
		TestAllCommentEventType.String(),
		RetestAllCommentEventType.String(),
		RetestSingleCommentEventType.String(),
		CancelCommentSingleEventType.String(),
		CancelCommentAllEventType.String(),
		OkToTestCommentEventType.String(),
		OnCommentEventType.String())
}

func getNameFromComment(typeOfComment, comment string) string {
	splitTest := strings.Split(strings.TrimSpace(comment), typeOfComment)
	if len(splitTest) < 2 {
		return ""
	}
	// now get the first line
	getFirstLine := strings.Split(splitTest[1], "\n")

	// and the first argument
	firstArg := strings.Split(getFirstLine[0], " ")
	if len(firstArg) < 2 {
		return ""
	}

	name := strings.TrimSpace(firstArg[1])
	if strings.Contains(name, "=") {
		return ""
	}
	return name
}
