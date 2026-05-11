//revive:disable-next-line:var-naming
package types

type UserWithMetadata struct {
	User               UserWithLinks `json:"user,omitempty"`
	Role               string        `json:"role,omitempty"`
	Approved           bool          `json:"approved,omitempty"`
	Status             string        `json:"status,omitempty"`
	LastReviewedCommit string        `json:"lastReviewedCommit,omitempty"`
}

type UserWithLinks struct {
	Name         string `json:"name,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
	ID           int    `json:"id,omitempty"`
	DisplayName  string `json:"displayName,omitempty"`
	Active       bool   `json:"active,omitempty"`
	Slug         string `json:"slug,omitempty"`
	Type         string `json:"type,omitempty"`
	Links        Links  `json:"links,omitempty"`
}

type PullRequest struct {
	ID           int                `json:"id"`
	Version      int32              `json:"version"`
	Title        string             `json:"title"`
	Description  string             `json:"description"`
	State        string             `json:"state"`
	Open         bool               `json:"open"`
	Closed       bool               `json:"closed"`
	CreatedDate  int64              `json:"createdDate"`
	UpdatedDate  int64              `json:"updatedDate"`
	FromRef      PullRequestRef     `json:"fromRef"`
	ToRef        PullRequestRef     `json:"toRef"`
	Locked       bool               `json:"locked"`
	Author       *UserWithMetadata  `json:"author,omitempty"`
	Reviewers    []UserWithMetadata `json:"reviewers"`
	Participants []UserWithMetadata `json:"participants,omitempty"`
	Properties   struct {
		MergeResult       MergeResult `json:"mergeResult"`
		ResolvedTaskCount int         `json:"resolvedTaskCount"`
		OpenTaskCount     int         `json:"openTaskCount"`
	} `json:"properties"`
	Links Links `json:"links"`
}

type PullRequestRef struct {
	ID           string     `json:"id"`
	DisplayID    string     `json:"displayId"`
	LatestCommit string     `json:"latestCommit"`
	Repository   Repository `json:"repository"`
}

type MergeResult struct {
	Outcome string `json:"outcome"`
	Current bool   `json:"current"`
}

type ActivityComment struct {
	Properties          Properties          `json:"properties"`
	ID                  int                 `json:"id"`
	Version             int                 `json:"version"`
	Text                string              `json:"text"`
	Author              User                `json:"author"`
	CreatedDate         int64               `json:"createdDate"`
	UpdatedDate         int64               `json:"updatedDate"`
	Comments            []ActivityComment   `json:"comments"`
	PermittedOperations PermittedOperations `json:"permittedOperations"`
}

type Properties struct {
	RepositoryID int `json:"repositoryId"`
}

type PermittedOperations struct {
	Editable  bool `json:"editable"`
	Deletable bool `json:"deletable"`
}

type Repository struct {
	Slug          string   `json:"slug,omitempty"`
	ID            int      `json:"id,omitempty"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
	ScmID         string   `json:"scmId,omitempty"`
	State         string   `json:"state,omitempty"`
	StatusMessage string   `json:"statusMessage,omitempty"`
	Forkable      bool     `json:"forkable,omitempty"`
	Project       *Project `json:"project,omitempty"`
	Public        bool     `json:"public,omitempty"`
	Links         *struct {
		Clone []CloneLink `json:"clone,omitempty"`
		Self  []SelfLink  `json:"self,omitempty"`
	} `json:"links,omitempty"`
	Owner *struct {
		Name         string `json:"name"`
		EmailAddress string `json:"emailAddress"`
		ID           int    `json:"id"`
		DisplayName  string `json:"displayName"`
		Active       bool   `json:"active"`
		Slug         string `json:"slug"`
		Type         string `json:"type"`
		AvatarURL    string `json:"avatarUrl"`
	} `json:"owner,omitempty"`
	Origin *Repository `json:"origin,omitempty"`
}

type Project struct {
	Key         string `json:"key"`
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
	Type        string `json:"type"`
	Links       Links  `json:"links"`
}

type SelfLink struct {
	Href string `json:"href"`
}

type CloneLink struct {
	Href string `json:"href"`
	Name string `json:"name"`
}

type Links struct {
	Self []SelfLink `json:"self,omitempty"`
}

type Commit struct {
	ID                 string `json:"id"`
	DisplayID          string `json:"displayId"`
	Author             User   `json:"author"`
	AuthorTimestamp    int64  `json:"authorTimestamp"`
	Committer          User   `json:"committer"`
	CommitterTimestamp int64  `json:"committerTimestamp"`
	Message            string `json:"message"`
	Parents            []struct {
		ID        string `json:"id"`
		DisplayID string `json:"displayId"`
	} `json:"parents"`
}

type User struct {
	Name                        string `json:"name"`
	EmailAddress                string `json:"emailAddress"`
	ID                          int    `json:"id"`
	DisplayName                 string `json:"displayName"`
	Active                      bool   `json:"active"`
	Slug                        string `json:"slug"`
	Type                        string `json:"type"`
	DirectoryName               string `json:"directoryName"`
	Deletable                   bool   `json:"deletable"`
	LastAuthenticationTimestamp int64  `json:"lastAuthenticationTimestamp"`
	MutableDetails              bool   `json:"mutableDetails"`
	MutableGroups               bool   `json:"mutableGroups"`
}

type PullRequestEvent struct {
	Actor       UserWithLinks `json:"actor"`
	PullRequest PullRequest   `json:"pullRequest"`
	EventKey    string        `json:"eventKey"`

	// Comment should be used when event is `pr:comment:added` or `pr:comment:edited`.
	Comment ActivityComment `json:"comment"`

	// CommentParentID and PreviousComment should be used when event is `pr:comment:edited`.
	CommentParentID string `json:"commentParentId"`
	PreviousComment string `json:"previousComment"`
}

type PushRequestEventChange struct {
	Ref      Ref    `json:"ref"`
	FromHash string `json:"fromHash"`
	ToHash   string `json:"toHash"`
	RefID    string `json:"refId"`
	Type     string `json:"type"`
}

type Ref struct {
	ID        string `json:"id"`
	DisplayID string `json:"displayId"`
	Type      string `json:"type"`
}

type PushRequestEvent struct {
	EventKey   string                   `json:"eventKey"`
	Actor      UserWithLinks            `json:"actor"`
	Repository Repository               `json:"repository"`
	Changes    []PushRequestEventChange `json:"changes"`
	Commits    []Commit                 `json:"commits"`
	ToCommit   ToCommit                 `json:"toCommit"`
}

type ToCommit struct {
	Commit
	Parents []Commit `json:"parents"` // Commit also has Parents field, but its Parents has only two fields while actual payload has more.
}

type Pagination struct {
	Start    int  `json:"start"`
	Size     int  `json:"size"`
	Limit    int  `json:"limit"`
	LastPage bool `json:"isLastPage"`
	NextPage int  `json:"nextPageStart"`
}

type DiffStats struct {
	Pagination
	Values []*DiffStat `json:"values"`
}

type DiffPath struct {
	Components []string `json:"components"`
	Parent     string   `json:"parent"`
	Name       string   `json:"name"`
	Extension  string   `json:"extension"`
	ToString   string   `json:"toString"`
}

type DiffStat struct {
	ContentID        string    `json:"contentId"`
	FromContentID    string    `json:"fromContentId"`
	Path             DiffPath  `json:"path"`
	SrcPath          *DiffPath `json:"srcPath,omitempty"`
	PercentUnchanged int       `json:"percentUnchanged"`
	Type             string    `json:"type"`
	NodeType         string    `json:"nodeType"`
	SrcExecutable    bool      `json:"srcExecutable"`
	Links            struct {
		Self []struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	Properties struct {
		GitChangeType string `json:"gitChangeType"`
	} `json:"properties"`
}
