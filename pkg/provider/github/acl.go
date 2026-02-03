package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/acl"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/policy"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
)

// CheckPolicyAllowing check that policy is allowing the event to be processed
// we  check the membership of the team allowed
// if the team is not found we explicitly disallow the policy, user have to correct the setting.
func (v *Provider) CheckPolicyAllowing(ctx context.Context, event *info.Event, allowedTeams []string) (bool, string) {
	for _, team := range allowedTeams {
		// TODO: caching
		opt := github.ListOptions{PerPage: v.PaginedNumber}
		for {
			members, resp, err := wrapAPI(v, "list_team_members_by_slug", func() ([]*github.User, *github.Response, error) {
				return v.Client().Teams.ListTeamMembersBySlug(ctx, event.Organization, team, &github.TeamListTeamMembersOptions{ListOptions: opt})
			})
			if resp.StatusCode == http.StatusNotFound {
				// we explicitly disallow the policy when the team is not found
				// maybe we should ignore it instead? i'd rather keep this explicit
				// and conservative since being security related.
				return false, fmt.Sprintf("team: %s is not found on the organization: %s", team, event.Organization)
			}
			if err != nil {
				// probably a 500 or another api error, no need to try again and again with other teams
				return false, fmt.Sprintf("error while getting team membership for user: %s in team: %s, error: %s", event.Sender, team, err.Error())
			}
			for _, member := range members {
				if member.GetLogin() == event.Sender {
					return true, fmt.Sprintf("allowing user: %s as a member of the team: %s", event.Sender, team)
				}
			}
			if resp.NextPage == 0 {
				break
			}
			opt.Page = resp.NextPage
		}
	}

	return false, fmt.Sprintf("user: %s is not a member of any of the allowed teams: %v", event.Sender, allowedTeams)
}

// IsAllowedOwnersFile get the owner files (OWNERS, OWNERS_ALIASES) from main branch
// and check if we have explicitly allowed the user in there.
func (v *Provider) IsAllowedOwnersFile(ctx context.Context, event *info.Event) (bool, error) {
	ownerContent, err := v.getFileFromDefaultBranch(ctx, "OWNERS", event)
	if err != nil {
		if strings.Contains(err.Error(), "cannot find") {
			// no owner file, skipping
			return false, nil
		}
		return false, err
	}
	// If there is OWNERS file, check for OWNERS_ALIASES
	ownerAliasesContent, err := v.getFileFromDefaultBranch(ctx, "OWNERS_ALIASES", event)
	if err != nil {
		if !strings.Contains(err.Error(), "cannot find") {
			return false, err
		}
	}

	return acl.UserInOwnerFile(ownerContent, ownerAliasesContent, event.Sender)
}

func (v *Provider) IsAllowed(ctx context.Context, event *info.Event) (bool, error) {
	aclPolicy := policy.Policy{
		Repository:   v.repo,
		EventEmitter: v.eventEmitter,
		Event:        event,
		VCX:          v,
		Logger:       v.Logger,
	}

	// Try to detect a policy rule allowing this
	tType, _ := v.detectTriggerTypeFromPayload("", event.Event)
	policyAllowed, policyReason := aclPolicy.IsAllowed(ctx, tType)

	switch policyAllowed {
	case policy.ResultAllowed:
		return true, nil
	case policy.ResultDisallowed:
		return false, nil
	case policy.ResultNotSet: // this is to make golangci-lint happy
	}

	// Check all the ACL rules
	allowed, err := v.aclCheckAll(ctx, event)
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}

	// Try to parse the comment from an owner who has issues a /ok-to-test
	ownerAllowed, err := v.aclAllowedOkToTestFromAnOwner(ctx, event)
	if err != nil {
		return false, err
	}
	if ownerAllowed {
		return true, nil
	}

	// error with the policy reason if it was set
	if policyReason != "" {
		return false, fmt.Errorf("%s", policyReason)
	}

	// finally silently return false if no rules allowed this
	return false, nil
}

// allowedOkToTestFromAnOwner Go over comments in a pull request and check
// if there is a /ok-to-test in there running an aclCheck again on the comment
// Sender if she is an OWNER and then allow it to run CI.
// TODO: pull out the github logic from there in an agnostic way.
func (v *Provider) aclAllowedOkToTestFromAnOwner(ctx context.Context, event *info.Event) (bool, error) {
	revent := info.NewEvent()
	event.DeepCopyInto(revent)
	revent.EventType = ""
	revent.TriggerTarget = ""
	if revent.Event == nil {
		return false, nil
	}

	switch event := revent.Event.(type) {
	case *github.IssueCommentEvent:
		// if we don't need to check old comments, then on issue comment we
		// need to check if comment have /ok-to-test and is from allowed user
		if !v.pacInfo.RememberOKToTest {
			return v.aclAllowedOkToTestCurrentComment(ctx, revent, event.Comment.GetID())
		}
		revent.URL = event.Issue.GetPullRequestLinks().GetHTMLURL()
	case *github.PullRequestEvent:
		// if we don't need to check old comments, then on push event we don't need
		// to check anything for the non-allowed user
		if !v.pacInfo.RememberOKToTest {
			return false, nil
		}
		revent.URL = event.GetPullRequest().GetHTMLURL()
	default:
		return false, nil
	}

	comments, err := v.GetStringPullRequestComment(ctx, revent)
	if err != nil {
		return false, err
	}

	for _, comment := range comments {
		revent.Sender = comment.User.GetLogin()
		allowed, err := v.aclCheckAll(ctx, revent)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// aclAllowedOkToTestCurrentEvent only check if this is issue comment event
// have /ok-to-test regex and sender is allowed.
func (v *Provider) aclAllowedOkToTestCurrentComment(ctx context.Context, revent *info.Event, id int64) (bool, error) {
	comment, _, err := wrapAPI(v, "get_issue_comment", func() (*github.IssueComment, *github.Response, error) {
		return v.Client().Issues.GetComment(ctx, revent.Organization, revent.Repository, id)
	})
	if err != nil {
		return false, err
	}

	gitOpsCommentPrefix := provider.GetGitOpsCommentPrefix(v.repo)

	if opscomments.IsOkToTestComment(comment.GetBody(), gitOpsCommentPrefix) {
		revent.Sender = comment.User.GetLogin()
		allowed, err := v.aclCheckAll(ctx, revent)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// aclCheck check if we are allowed to run the pipeline on that PR.
func (v *Provider) aclCheckAll(ctx context.Context, rev *info.Event) (bool, error) {
	// if the sender own the repo, then allow it to run
	if rev.Organization == rev.Sender {
		return true, nil
	}

	// If the user who has submitted the PR is not a owner or public member or Collaborator or not there in OWNERS file
	// but has permission to push to branches then allow the CI to be run.
	// This can only happen with GithubApp and Bots.
	// Ex: dependabot, bots
	if rev.PullRequestNumber != 0 {
		isSameCloneURL, err := v.checkPullRequestForSameURL(ctx, rev)
		if err != nil {
			return false, err
		}
		if isSameCloneURL {
			return true, nil
		}
	}

	// If the user who has submitted the pr is a owner on the repo then allows
	// the CI to be run.
	isUserMemberRepo, err := v.checkSenderOrgMembership(ctx, rev)
	if err != nil {
		return false, err
	}
	if isUserMemberRepo {
		return true, nil
	}

	checkSenderRepoMembership, err := v.checkSenderRepoMembership(ctx, rev)
	if err != nil {
		return false, err
	}
	if checkSenderRepoMembership {
		return true, nil
	}

	// If we have a prow OWNERS file in the defaultBranch (ie: master) then
	// parse it in approvers and reviewers field and check if sender is in there.
	return v.IsAllowedOwnersFile(ctx, rev)
}

// checkPullRequestForSameURL checks If PullRequests are for same clone URL and different branches
// means if the user has access to create a branch in the repository without forking or having any permissions then PAC should allow to run CI.
//
//	ex: dependabot, *[bot] etc...
func (v *Provider) checkPullRequestForSameURL(ctx context.Context, runevent *info.Event) (bool, error) {
	pr, resp, err := wrapAPI(v, "get_pull_request", func() (*github.PullRequest, *github.Response, error) {
		return v.Client().PullRequests.Get(ctx, runevent.Organization, runevent.Repository, runevent.PullRequestNumber)
	})
	if err != nil {
		return false, err
	}
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	if pr.GetHead().GetRepo().GetCloneURL() == pr.GetBase().GetRepo().GetCloneURL() && pr.GetHead().GetRef() != pr.GetBase().GetRef() {
		return true, nil
	}

	return false, nil
}

// checkSenderOrgMembership Get sender user's organization. We can
// only get the one that the user sets as public 🤷.
func (v *Provider) checkSenderOrgMembership(ctx context.Context, runevent *info.Event) (bool, error) {
	opt := &github.ListMembersOptions{
		ListOptions: github.ListOptions{PerPage: v.PaginedNumber},
	}

	for {
		users, resp, err := wrapAPI(v, "list_org_members", func() ([]*github.User, *github.Response, error) {
			return v.Client().Organizations.ListMembers(ctx, runevent.Organization, opt)
		})
		// If we are 404 it means we are checking a repo owner and not a org so let's bail out with grace
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}

		if err != nil {
			return false, err
		}
		for _, v := range users {
			if v.GetLogin() == runevent.Sender {
				return true, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return false, nil
}

// checkSenderRepoMembership check if user is allowed to run CI.
func (v *Provider) checkSenderRepoMembership(ctx context.Context, runevent *info.Event) (bool, error) {
	isCollab, _, err := wrapAPI(v, "is_collaborator", func() (bool, *github.Response, error) {
		return v.Client().Repositories.IsCollaborator(ctx,
			runevent.Organization,
			runevent.Repository,
			runevent.Sender)
	})

	return isCollab, err
}

// getFileFromDefaultBranch will get a file directly from the Default BaseBranch as
// configured in runinfo which is directly set in webhook by Github.
func (v *Provider) getFileFromDefaultBranch(ctx context.Context, path string, runevent *info.Event) (string, error) {
	tektonyaml, err := v.GetFileInsideRepo(ctx, runevent, path, runevent.DefaultBranch)
	if err != nil {
		return "", fmt.Errorf("cannot find %s inside the %s branch: %w", path, runevent.DefaultBranch, err)
	}
	return tektonyaml, err
}

// GetStringPullRequestComment return the comment if we find an /ok-to-test comment in one of
// the comments text of a pull request.
func (v *Provider) GetStringPullRequestComment(ctx context.Context, runevent *info.Event) ([]*github.IssueComment, error) {
	var ret []*github.IssueComment
	prNumber, err := convertPullRequestURLtoNumber(runevent.URL)
	if err != nil {
		return nil, err
	}

	opt := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: v.PaginedNumber},
	}

	gitOpsCommentPrefix := provider.GetGitOpsCommentPrefix(v.repo)

	for {
		comments, resp, err := wrapAPI(v, "list_issue_comments", func() ([]*github.IssueComment, *github.Response, error) {
			return v.Client().Issues.ListComments(ctx, runevent.Organization, runevent.Repository,
				prNumber, opt)
		})
		if err != nil {
			return nil, err
		}
		for _, v := range comments {
			if opscomments.IsOkToTestComment(v.GetBody(), gitOpsCommentPrefix) {
				ret = append(ret, v)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return ret, nil
}
