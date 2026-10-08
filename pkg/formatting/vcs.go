package formatting

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// SanitizeBranch remove refs/heads from string, only removing the first prefix
// in case we have branch that are actually called refs-heads 🙃.
func SanitizeBranch(s string) string {
	if branch, ok := strings.CutPrefix(s, "refs/heads/"); ok {
		return branch
	}
	if branch, ok := strings.CutPrefix(s, "refs-heads-"); ok {
		return branch
	}
	return s
}

var shortShaLength = 7

// ShortSHA returns a shortsha, shorter values are returned as is.
func ShortSHA(sha string) string {
	if len(sha) < shortShaLength-1 {
		return sha
	}
	return sha[0:shortShaLength]
}

func GetRepoOwnerFromURL(ghURL string) (string, error) {
	org, repo, err := GetRepoOwnerSplitted(ghURL)
	if err != nil {
		return "", err
	}
	repo = strings.TrimSuffix(repo, "/")
	return strings.ToLower(fmt.Sprintf("%s/%s", org, repo)), nil
}

func GetRepoOwnerSplitted(u string) (string, string, error) {
	uparse, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(uparse.Path, "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("invalid repo url at least a organization/project and a repo needs to be specified: %s", u)
	}
	org := filepath.Join(parts[0 : len(parts)-1]...)
	repo := parts[len(parts)-1]
	return org, repo, nil
}

// CamelCasit pull_request > PullRequest.
func CamelCasit(s string) string {
	c := cases.Title(language.AmericanEnglish)
	return strings.ReplaceAll(c.String(strings.ReplaceAll(s, "_", " ")), " ", "")
}
