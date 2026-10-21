package triggertype

type (
	Trigger string
)

// IsPullRequestType all Triggertype that are actually a pull request.
func IsPullRequestType(s string) Trigger {
	eventType := s
	switch s {
	case PullRequest.String(), OkToTest.String(), Retest.String(), Cancel.String(), PullRequestLabeled.String():
		eventType = PullRequest.String()
	}
	return Trigger(eventType)
}

func (t Trigger) String() string {
	return string(t)
}

func StringToType(s string) Trigger {
	switch s {
	case OkToTest.String():
		return OkToTest
	case Retest.String():
		return Retest
	case Push.String():
		return Push
	case PullRequest.String():
		return PullRequest
	case Cancel.String():
		return Cancel
	case CheckSuiteRerequested.String():
		return CheckSuiteRerequested
	case CheckRunRerequested.String():
		return CheckRunRerequested
	case Incoming.String():
		return Incoming
	case Comment.String():
		return Comment
	case PullRequestLabeled.String():
		return PullRequestLabeled
	case Release.String():
		return Release
	}
	return ""
}

const (
	Cancel                Trigger = "cancel"
	CheckRunRerequested   Trigger = "check-run-rerequested"
	CheckSuiteRerequested Trigger = "check-suite-rerequested"
	Comment               Trigger = "comment"
	Incoming              Trigger = "incoming"
	PullRequestLabeled    Trigger = "pull_request_labeled"
	OkToTest              Trigger = "ok-to-test"
	PullRequestClosed     Trigger = "pull_request_closed"
	Release               Trigger = "release"
	PullRequest           Trigger = "pull_request" // it's should be "pull_request_opened_updated" but let's keep it simple.
	Push                  Trigger = "push"
	Retest                Trigger = "retest"
)
