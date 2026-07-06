package gitea

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	giteaStructs "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/sdk/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/acl"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/policy"
)

func (v *Provider) CheckPolicyAllowing(_ context.Context, event *info.Event, allowedTeams []string) (bool, string) {
	if event.Organization == event.Repository {
		return true, ""
	}
	// TODO: caching
	orgTeams, resp, err := v.Client.ListOrgTeams(event.Organization, gitea.ListTeamsOptions{})
	if resp.StatusCode == http.StatusNotFound {
		// we explicitly disallow the policy when there is no team on org
		return false, fmt.Sprintf("no teams on org %s", event.Organization)
	}
	if err != nil {
		// probably a 500 or another api error, no need to try again and again with other teams
		return false, fmt.Sprintf("error while getting org team, error: %s", err.Error())
	}
	for _, allowedTeam := range allowedTeams {
		for _, orgTeam := range orgTeams {
			if orgTeam.Name == allowedTeam {
				teamMember, _, err := v.Client.GetTeamMember(orgTeam.ID, event.Sender)
				if err != nil {
					v.Logger.Infof("error while getting team member: %s, error: %s", event.Sender, err.Error())
					continue
				}
				if teamMember.ID != 0 {
					return true, fmt.Sprintf("allowing user: %s as a member of the team: %s", event.Sender, orgTeam.Name)
				}
			}
		}
	}
	return false, fmt.Sprintf("user: %s is not a member of any of the allowed teams: %v", event.Sender, allowedTeams)
}

func (v *Provider) IsAllowed(ctx context.Context, event *info.Event) (bool, error) {
	aclPolicy := policy.Policy{
		Repository:   v.repo,
		EventEmitter: v.eventEmitter,
		Event:        event,
		VCX:          v,
		Logger:       v.Logger,
	}

	// Try to detect a policy rule allowed it
	tType, _ := detectTriggerTypeFromPayload("", event.Event)
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
	case *giteaStructs.IssueCommentPayload:
		// if we don't need to check old comments, then on issue comment we
		// need to check if comment have /ok-to-test and is from allowed user
		if !v.pacInfo.RememberOKToTest {
			return v.aclAllowedOkToTestCurrentComment(ctx, revent, event.Comment.ID)
		}
		revent.URL = event.Issue.URL
	case *giteaStructs.PullRequestPayload:
		// if we don't need to check old comments, then on push event we don't need
		// to check anything for the non-allowed user
		if !v.pacInfo.RememberOKToTest {
			return false, nil
		}
		revent.URL = event.PullRequest.HTMLURL
	default:
		return false, nil
	}

	comments, err := v.GetStringPullRequestComment(ctx, revent, acl.OKToTestCommentRegexp)
	if err != nil {
		return false, err
	}

	for _, comment := range comments {
		revent.Sender = comment.Poster.UserName
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
	comment, _, err := v.Client.GetIssueComment(revent.Organization, revent.Repository, id)
	if err != nil {
		return false, err
	}
	if acl.MatchRegexp(acl.OKToTestCommentRegexp, comment.Body) {
		revent.Sender = comment.Poster.UserName
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
	if rev.Organization == rev.Sender {
		return true, nil
	}

	checkSenderRepoMembership, err := v.checkSenderRepoMembership(ctx, rev)
	if err != nil {
		return false, err
	}
	if checkSenderRepoMembership {
		return true, nil
	}

	return v.IsAllowedOwnersFile(ctx, rev)
}

// IsAllowedOwnersFile get the OWNERS files from main branch and check if we have
// explicitly allowed the user in there.
func (v *Provider) IsAllowedOwnersFile(ctx context.Context, rev *info.Event) (bool, error) {
	// If we have a OWNERS and OWNERS_ALIASE files in the defaultBranch (ie: master) then
	// parse them and check if sender is in there.
	ownerContent, err := v.getFileFromDefaultBranch(ctx, "OWNERS", rev)
	if err != nil {
		if strings.Contains(err.Error(), "cannot find") {
			// no owner file, skipping
			return false, nil
		}
		return false, err
	}
	// If there is OWNERS file, check for OWNERS_ALIASES. OWNERS can exist without OWNERS_ALIASES.
	// OWNERS_ALIASES can't exist without OWNERS.
	ownerAliasesContent, err := v.getFileFromDefaultBranch(ctx, "OWNERS_ALIASES", rev)
	if err != nil {
		if !strings.Contains(err.Error(), "cannot find") {
			return false, err
		}
	}

	return acl.UserInOwnerFile(ownerContent, ownerAliasesContent, rev.Sender)
}

func (v *Provider) checkSenderRepoMembership(_ context.Context, runevent *info.Event) (bool, error) {
	ret, _, err := v.Client.IsCollaborator(runevent.Organization, runevent.Repository, runevent.Sender)
	return ret, err
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

// GetStringPullRequestComment return the comment if we find a regexp in one of
// the comments text of a pull request.
func (v *Provider) GetStringPullRequestComment(_ context.Context, runevent *info.Event, reg string) ([]*gitea.Comment, error) {
	var ret []*gitea.Comment
	prNumber, err := convertPullRequestURLtoNumber(runevent.URL)
	if err != nil {
		return nil, err
	}

	comments, _, err := v.Client.ListIssueComments(runevent.Organization, runevent.Repository, int64(prNumber), gitea.ListIssueCommentOptions{})
	if err != nil {
		return nil, err
	}
	for _, v := range comments {
		if acl.MatchRegexp(reg, v.Body) {
			ret = append(ret, v)
		}
	}
	return ret, nil
}
