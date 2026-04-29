//revive:disable-next-line:var-naming
package types

type Workspace struct {
	Slug string `json:"slug"`
}

type Repository struct {
	Workspace Workspace `json:"workspace"`
	Name      string    `json:"name"`
	FullName  string    `json:"full_name"`
	Links     Links     `json:"links"`
}

type HTMLLink struct {
	HRef string `json:"href"`
}

type Links struct {
	HTML HTMLLink `json:"html"`
}

type Author struct {
	AccountID string `json:"account_id"`
	User      User   `json:"user"`
	Nickname  string `json:"nickname,omitempty"`
}

type Branch struct {
	Name string `json:"name"`
}

type Destination struct {
	Branch     Branch     `json:"branch"`
	Commit     Commit     `json:"commit"`
	Repository Repository `json:"repository"`
}

type Commit struct {
	Hash    string `json:"hash"`
	Links   Links  `json:"links"`
	Message string `json:"message"`
	Author  Author `json:"author"`
}

type Source struct {
	Branch     Branch     `json:"branch"`
	Commit     Commit     `json:"commit"`
	Repository Repository `json:"repository"`
}

type PullRequest struct {
	Author      Author      `json:"author"`
	Destination Destination `json:"destination"`
	Source      Source      `json:"source"`
	ID          int         `json:"id"`
	Links       Links
	Title       string `json:"title"`
	State       string `json:"state"`
}

type PullRequestEvent struct {
	Repository  Repository  `json:"repository"`
	PullRequest PullRequest `json:"pullrequest"`
	Comment     Comment     `json:"comment"`
}

type Push struct {
	Changes []Change `json:"changes"`
}

type PushRequestEvent struct {
	Repository Repository
	Actor      User
	Push       Push `json:"push"`
}

type ChangeType struct {
	Name   string
	Target Commit
	Type   string
}

type Change struct {
	New ChangeType
	Old ChangeType
}

type User struct {
	DisplayName string `json:"display_name" mapstructure:"display_name"`
	AccountID   string `json:"account_id"   mapstructure:"account_id"`
	Nickname    string
}

// IPRangesItem https://ip-ranges.atlassian.com/
type IPRangesItem struct {
	Network string
	CIDR    string
	MaskLen string // `json:"mask_len"`
	Mask    string
}

type IPRanges struct {
	Items []IPRangesItem
}

type Member struct {
	User User `json:"user"`
}

type Members struct {
	Values []Member
}

type Content struct {
	Raw string `json:"raw"`
}

type Comment struct {
	Content Content `json:"content"`
	User    User
}

type Comments struct {
	Values []Comment
}

type Status struct {
	Key         string     `json:"key"`
	Type        string     `json:"type"`
	State       string     `json:"state"`
	Name        string     `json:"name"`
	RefName     string     `json:"refname"`
	Commit      Commit     `json:"commit"`
	URL         string     `json:"url"`
	Repository  Repository `json:"repository"`
	Description string     `json:"description"`
	Links       Links      `json:"links"`
}

type CommitStatusState string

// See: https://api.bitbucket.org/swagger.json attribute:
// "definitions.commitStatus.allOf[1].properties.state.enum" for
// specification.
const (
	StateStopped    CommitStatusState = "STOPPED"
	StateFailed     CommitStatusState = "FAILED"
	StateInProgress CommitStatusState = "INPROGRESS"
	StateSuccessful CommitStatusState = "SUCCESSFUL"
)
