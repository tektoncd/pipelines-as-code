package gitlab

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/acl"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// IsAllowedOwnersFile get the owner files (OWNERS, OWNERS_ALIASES) from main branch
// and check if we have explicitly allowed the user in there.
func (v *Provider) IsAllowedOwnersFile(_ context.Context, event *info.Event) (bool, error) {
	ownerContent, _, _ := v.getObject("OWNERS", event.DefaultBranch, v.targetProjectID)
	if string(ownerContent) == "" {
		return false, nil
	}
	// OWNERS_ALIASES file existence is not required, if we get "not found" continue
	ownerAliasesContent, resp, err := v.getObject("OWNERS_ALIASES", event.DefaultBranch, v.targetProjectID)
	if err := ownersAliasesResponseError(resp, err); err != nil {
		return false, err
	}
	allowed, _ := acl.UserInOwnerFile(string(ownerContent), string(ownerAliasesContent), event.Sender)
	return allowed, nil
}

func ownersAliasesResponseError(resp *gitlab.Response, err error) error {
	if resp == nil || resp.Response == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("gitlab API returned no response for OWNERS_ALIASES")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d while fetching OWNERS_ALIASES", resp.StatusCode)
	}
	return nil
}

func (v *Provider) checkMembership(ctx context.Context, event *info.Event, userid int64) bool {
	// Initialize cache lazily
	if v.memberCache == nil {
		v.memberCache = map[int64]bool{}
	}

	if allowed, ok := v.memberCache[userid]; ok {
		return allowed
	}

	member, _, err := v.Client().ProjectMembers.GetInheritedProjectMember(v.targetProjectID, userid)
	if err != nil {
		// If the API call fails, fall back without caching the result so a
		// transient failure can be retried on the next invocation.
		isAllowed, _ := v.IsAllowedOwnersFile(ctx, event)
		return isAllowed
	}

	if member.ID != 0 && member.ID == userid {
		v.memberCache[userid] = true
		return true
	}

	isAllowed, _ := v.IsAllowedOwnersFile(ctx, event)
	v.memberCache[userid] = isAllowed
	return isAllowed
}

func (v *Provider) checkOkToTestCommentFromApprovedMember(ctx context.Context, event *info.Event, page int64) (bool, error) {
	switch gitEvent := event.Event.(type) {
	case *gitlab.MergeEvent:
		if !v.pacInfo.RememberOKToTest {
			v.Logger.Debug("RememberOKToTest is disabled, skipping MergeRequest notes check as it is not needed")
			return false, nil
		}
	case *gitlab.MergeCommentEvent:
		if !v.pacInfo.RememberOKToTest {
			v.Logger.Debug("Event is a MergeCommentEvent and RememberOKToTest is disabled, checking current comment only")
			return v.aclAllowedOkToTestCurrentComment(ctx, event, gitEvent.ObjectAttributes.ID)
		}
	default:
		v.Logger.Debug("Event is not a MergeEvent or MergeCommentEvent, skipping merge request notes check")
		return false, nil
	}

	var nextPage int64
	opt := &gitlab.ListMergeRequestDiscussionsOptions{
		ListOptions: gitlab.ListOptions{
			Page:    page,
			PerPage: defaultGitlabListOptions.PerPage,
		},
	}
	discussions, resp, err := v.Client().Discussions.ListMergeRequestDiscussions(v.targetProjectID, int64(event.PullRequestNumber), opt)
	if err != nil || len(discussions) == 0 {
		return false, err
	}
	if resp.NextPage != 0 {
		nextPage = resp.NextPage
	}

	for _, discussion := range discussions {
		// Iterate through every note in the discussion thread and evaluate them.
		// If a note contains an OK-to-test command, verify the commenter's permission
		// (either project membership or presence in OWNERS/OWNERS_ALIASES).
		for _, note := range discussion.Notes {
			if acl.MatchRegexp(acl.OKToTestCommentRegexp, note.Body) {
				commenterEvent := info.NewEvent()
				commenterEvent.Event = event.Event
				commenterEvent.Sender = note.Author.Username
				commenterEvent.BaseBranch = event.BaseBranch
				commenterEvent.HeadBranch = event.HeadBranch
				commenterEvent.DefaultBranch = event.DefaultBranch
				// We could add caching for membership checks in the future.
				if v.checkMembership(ctx, commenterEvent, note.Author.ID) {
					return true, nil
				}
			}
		}
	}

	if nextPage != 0 {
		return v.checkOkToTestCommentFromApprovedMember(ctx, event, nextPage)
	}

	return false, nil
}

func (v *Provider) aclAllowedOkToTestCurrentComment(ctx context.Context, event *info.Event, commentID int64) (bool, error) {
	comment, _, err := v.Client().Notes.GetMergeRequestNote(v.targetProjectID, int64(event.PullRequestNumber), commentID)
	if err != nil {
		return false, err
	}

	if acl.MatchRegexp(acl.OKToTestCommentRegexp, comment.Body) {
		commenterEvent := info.NewEvent()
		commenterEvent.Event = event.Event
		commenterEvent.Sender = comment.Author.Username
		commenterEvent.BaseBranch = event.BaseBranch
		commenterEvent.HeadBranch = event.HeadBranch
		commenterEvent.DefaultBranch = event.DefaultBranch
		if v.checkMembership(ctx, commenterEvent, comment.Author.ID) {
			return true, nil
		}
	}
	return false, nil
}

func (v *Provider) IsAllowed(ctx context.Context, event *info.Event) (bool, error) {
	if v.gitlabClient == nil {
		return false, fmt.Errorf("no github client has been initialized, " +
			"exiting... (hint: did you forget setting a secret on your repo?)")
	}
	if v.checkMembership(ctx, event, v.userID) {
		return true, nil
	}

	return v.checkOkToTestCommentFromApprovedMember(ctx, event, 1)
}
