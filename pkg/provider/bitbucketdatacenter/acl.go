package bitbucketdatacenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/tektoncd/pipelines-as-code/pkg/acl"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"

	"github.com/jenkins-x/go-scm/scm"
)

func (v *Provider) IsAllowed(ctx context.Context, event *info.Event) (bool, error) {
	allowed, err := v.checkMemberShip(ctx, event)
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}

	// Check then from comment if there is a approved user that has done a /ok-to-test
	return v.checkOkToTestCommentFromApprovedMember(ctx, event)
}

// IsAllowedOwnersFile get the owner files (OWNERS, OWNERS_ALIASES) from main branch
// and check if we have explicitly allowed the user in there.
func (v *Provider) IsAllowedOwnersFile(ctx context.Context, event *info.Event) (bool, error) {
	ownerContent, err := v.GetFileInsideRepo(ctx, event, "OWNERS", event.DefaultBranch)
	if err != nil {
		return false, err
	}
	ownerAliasesContent, err := v.GetFileInsideRepo(ctx, event, "OWNERS_ALIASES", event.DefaultBranch)
	if err != nil {
		if !strings.Contains(err.Error(), "cannot find") {
			return false, err
		}
	}

	return acl.UserInOwnerFile(ownerContent, ownerAliasesContent, event.AccountID)
}

func (v *Provider) checkOkToTestCommentFromApprovedMember(ctx context.Context, event *info.Event) (bool, error) {
	allComments := []*scm.Comment{}
	OrgAndRepo := fmt.Sprintf("%s/%s", event.Organization, event.Repository)
	opts := &scm.ListOptions{Page: 1, Size: apiResponseLimit}
	for {
		comments, _, err := v.Client().PullRequests.ListComments(ctx, OrgAndRepo, v.pullRequestNumber, opts)
		if err != nil {
			return false, err
		}

		allComments = append(allComments, comments...)

		if len(comments) < apiResponseLimit {
			break
		}

		opts.Page++
	}

	for _, comment := range allComments {
		if acl.MatchRegexp(acl.OKToTestCommentRegexp, comment.Body) {
			commenterEvent := info.NewEvent()
			commenterEvent.Sender = comment.Author.Login
			commenterEvent.AccountID = fmt.Sprintf("%d", comment.Author.ID)
			commenterEvent.Event = event.Event
			commenterEvent.BaseBranch = event.BaseBranch
			commenterEvent.HeadBranch = event.HeadBranch
			commenterEvent.Repository = event.Repository
			commenterEvent.Organization = v.projectKey
			commenterEvent.DefaultBranch = event.DefaultBranch
			allowed, err := v.checkMemberShip(ctx, commenterEvent)
			if err != nil {
				return false, err
			}
			if allowed {
				// TODO: show a log how come this has been allowed
				return true, nil
			}
		}
	}
	return false, nil
}

func (v *Provider) checkMemberShip(ctx context.Context, event *info.Event) (bool, error) {
	// Get permissions from project
	allowed, _, err := v.Client().Organizations.IsMember(ctx, event.Organization, event.Sender)
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}

	orgAndRepo := fmt.Sprintf("%s/%s", event.Organization, event.Repository)
	// Get permissions from repo
	allowed, _, err = v.Client().Repositories.IsCollaborator(ctx, orgAndRepo, event.Sender)
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}

	// Check if sender is inside the Owner file
	// in the 'main' branch Silently ignore error, which should be fine it
	// probably means the OWNERS file is not created. If we had another error
	// (ie: like API) we probably would have hit it already.
	allowed, err = v.IsAllowedOwnersFile(ctx, event)
	if allowed {
		return true, err
	}

	return false, nil
}
