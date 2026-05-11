package test

import "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"

type ProjGroup struct {
	Group      Group  `json:"group"`
	Permission string `json:"permission"`
}

type Group struct {
	Name string `json:"name"`
}

// GroupMember matches the member struct in go-scm's stash driver,
// returned by the /admin/groups/more-members endpoint.
type GroupMember struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// kept these structs here because these are only used in tests

type Comment struct {
	Text   string  `json:"text"`
	Parent *Parent `json:"parent,omitempty"`
	Anchor *Anchor `json:"anchor,omitempty"`
}

type Parent struct {
	ID int `json:"id"`
}

type DiffType string

type LineType string

type FileType string

type Anchor struct {
	DiffType DiffType `json:"diffType,omitempty"`
	Line     int      `json:"line,omitempty"`
	LineType LineType `json:"lineType,omitempty"`
	FileType FileType `json:"fileType,omitempty"`
	FromHash string   `json:"fromHash,omitempty"`
	Path     string   `json:"path,omitempty"`
	SrcPath  string   `json:"srcPath,omitempty"`
	ToHash   string   `json:"toHash,omitempty"`
}

type Branch struct {
	ID              string `json:"id"`
	DisplayID       string `json:"displayId"`
	Type            string `json:"type"`
	LatestCommit    string `json:"latestCommit"`
	LatestChangeset string `json:"latestChangeset"`
	IsDefault       bool   `json:"isDefault"`
}

type BuildStatus struct {
	State       string `json:"state"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	DateAdded   int64  `json:"dateAdded"`
}

type UserPermission struct {
	User       types.User `json:"user"`
	Permission string     `json:"permission"`
}

type Action string

type Activity struct {
	ID               int                   `json:"id"`
	CreatedDate      int                   `json:"createdDate"`
	User             types.User            `json:"user"`
	Action           Action                `json:"action"`
	CommentAction    string                `json:"commentAction"`
	Comment          types.ActivityComment `json:"comment"`
	CommentAnchor    Anchor                `json:"commentAnchor"`
	FromHash         string                `json:"fromHash,omitempty"`
	PreviousFromHash string                `json:"previousFromHash,omitempty"`
	PreviousToHash   string                `json:"previousToHash,omitempty"`
	ToHash           string                `json:"toHash,omitempty"`
	Added            CommitsStats          `json:"added"`
	Removed          CommitsStats          `json:"removed"`
}

type CommitsStats struct {
	Commits []types.Commit `json:"commits"`
	Total   int            `json:"total"`
}
