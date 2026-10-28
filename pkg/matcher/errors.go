package matcher

import (
	"context"
	"fmt"
	"strings"

	apipac "github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacerrors "github.com/tektoncd/pipelines-as-code/pkg/errors"
	"github.com/tektoncd/pipelines-as-code/pkg/events"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/provider"

	"go.uber.org/zap"
)

// checkCELEvaluateError checks if error is from CEL evaluation stages.
func checkIfCELEvaluateError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	patterns := []string{
		`failed to parse expression`,
		`check failed`,
		`failed to create a Program`,
		`failed to evaluate`,
	}

	for _, pattern := range patterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

func reportCELValidationErrors(ctx context.Context, repo *apipac.Repository, validationErrors []*pacerrors.PacYamlValidations, eventEmitter *events.EventEmitter, vcx provider.Interface, event *info.Event, prefix string) {
	errorRows := make([]string, 0, len(validationErrors))
	for _, err := range validationErrors {
		errorRows = append(errorRows, fmt.Sprintf("| %s | `%s` |", err.Name, err.Err.Error()))
	}
	if len(errorRows) == 0 {
		return
	}

	var err error

	// The prefix is used for a single comment "update" strategy where the same comment is updated
	// with the celValidationError and the PipelineRun status for any subsequent or prior "runs".
	if prefix != "" {
		markdownErrMessage := fmt.Sprintf(`%s
%s
%s`, prefix, provider.ValidationErrorTemplate, strings.Join(errorRows, "\n"))
		err = vcx.CreateComment(ctx, event, markdownErrMessage, prefix)
	} else {
		markdownErrMessage := fmt.Sprintf(`%s
%s`, provider.ValidationErrorTemplate, strings.Join(errorRows, "\n"))
		err = vcx.CreateComment(ctx, event, markdownErrMessage, provider.ValidationErrorTemplate)
	}
	if err != nil {
		eventEmitter.EmitMessage(repo, zap.ErrorLevel, "PipelineRunCommentCreationError",
			fmt.Sprintf("failed to create comment: %s", err.Error()))
	}
}

// sanitizeErrorAsMarkdown prepares a CEL evaluation error string to be rendered
// inside a GitHub / GitLab markdown table without breaking its layout.
//
// Markdown tables use the vertical bar character (`|`) as a column delimiter. If
// the original error message contains an un-escaped pipe the markdown renderer
// interprets it as the start of a new column or row which distorts the table
// produced by Pipelines-as-Code when reporting validation errors.
//
// To avoid this we escape every pipe with a backslash (\|). We also replace any
// newline or carriage-return characters with a single space so that the whole
// error is kept on one row, preserving readability in the rendered comment.
func sanitizeErrorAsMarkdown(err error) string {
	errStr := err.Error()
	errStr = strings.ReplaceAll(errStr, "|", "\\|")
	errStr = strings.ReplaceAll(errStr, "\n", " ")
	errStr = strings.ReplaceAll(errStr, "\r", " ")
	return errStr
}
