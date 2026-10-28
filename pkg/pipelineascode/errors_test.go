package pipelineascode

import (
	"context"
	"errors"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacerrors "github.com/tektoncd/pipelines-as-code/pkg/errors"
	"github.com/tektoncd/pipelines-as-code/pkg/events"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	providerstatus "github.com/tektoncd/pipelines-as-code/pkg/provider/status"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/tektoncd/pipelines-as-code/pkg/test/provider"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCheckAccessOrErrror(t *testing.T) {
	tests := []struct {
		name              string
		allowIt           bool
		sender            string
		accountID         string
		createStatusError bool
		expectedErr       bool
		expectedAllowed   bool
		expectedErrMsg    string
	}{
		{
			name:            "user is allowed",
			allowIt:         true,
			expectedAllowed: true,
		},
		{
			name:            "user is not allowed - no account ID",
			allowIt:         false,
			sender:          "johndoe",
			expectedAllowed: false,
		},
		{
			name:            "user is not allowed - with account ID",
			allowIt:         false,
			sender:          "johndoe",
			accountID:       "user123",
			expectedAllowed: false,
		},
		{
			name:              "create status error",
			allowIt:           false,
			sender:            "johndoe",
			createStatusError: true,
			expectedErr:       true,
			expectedErrMsg:    "failed to run create status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup observer to capture logs
			observerCore, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observerCore).Sugar()

			// Create test event
			testEvent := &info.Event{
				Sender:    tt.sender,
				AccountID: tt.accountID,
			}

			// Create mock provider
			prov := &testprovider.TestProviderImp{
				AllowIT: tt.allowIt,
			}

			// Set createStatus error if needed
			if tt.createStatusError {
				prov.CreateStatusErorring = true
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			// Create mock event emitter
			eventEmitter := events.NewEventEmitter(stdata.Kube, logger)

			// Create PacRun
			p := &PacRun{
				event:        testEvent,
				vcx:          prov,
				logger:       logger,
				eventEmitter: eventEmitter,
			}

			// Call the function
			repo := &v1alpha1.Repository{}
			status := providerstatus.StatusOpts{}
			allowed, err := p.checkAccessOrError(context.Background(), repo, status, "via test")

			// Verify results
			if tt.expectedErr {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.expectedErrMsg != "" {
					assert.Assert(t, err.Error() != "", "Expected error message but got empty string")
					assert.ErrorContains(t, err, tt.expectedErrMsg)
				}
			} else {
				assert.NilError(t, err)
			}

			assert.Equal(t, tt.expectedAllowed, allowed)
		})
	}
}

func TestReportValidationErrors(t *testing.T) {
	tests := []struct {
		name                  string
		validationErrors      []*pacerrors.PacYamlValidations
		expectCommentCreation bool
	}{
		{
			name:                  "no validation errors",
			validationErrors:      []*pacerrors.PacYamlValidations{},
			expectCommentCreation: false,
		},
		{
			name: "tekton validation errors",
			validationErrors: []*pacerrors.PacYamlValidations{
				{
					Name:   "test-pipeline-1",
					Err:    errors.New("invalid pipeline spec"),
					Schema: "tekton.dev",
				},
			},
			expectCommentCreation: true,
		},
		{
			name: "non-tekton schema errors",
			validationErrors: []*pacerrors.PacYamlValidations{
				{
					Name:   "test-other",
					Err:    errors.New("some error"),
					Schema: "other.schema",
				},
			},
			expectCommentCreation: false,
		},
		{
			name: "ignored errors by regex",
			validationErrors: []*pacerrors.PacYamlValidations{
				{
					Name:   "test-ignored",
					Err:    errors.New("no kind test is registered for version v1 in scheme"),
					Schema: "tekton.dev",
				},
			},
			expectCommentCreation: false,
		},
		{
			name: "create comment error",
			validationErrors: []*pacerrors.PacYamlValidations{
				{
					Name:   "test-pipeline",
					Err:    errors.New("validation error"),
					Schema: "tekton.dev",
				},
			},
			expectCommentCreation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup observer to capture logs
			observerCore, logs := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observerCore).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)

			// Create test event
			testEvent := &info.Event{}

			// Create mock provider
			prov := &testprovider.TestProviderImp{}

			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})

			// Create mock event emitter
			eventEmitter := events.NewEventEmitter(stdata.Kube, logger)
			// Create PacRun
			p := &PacRun{
				event:        testEvent,
				vcx:          prov,
				logger:       logger,
				eventEmitter: eventEmitter,
			}

			// Call the function
			repo := &v1alpha1.Repository{}
			p.reportValidationErrors(context.Background(), repo, tt.validationErrors)

			// Verify results
			// Verify log messages for validation errors
			logEntries := logs.All()
			errorLogCount := 0
			for _, entry := range logEntries {
				if entry.Level == zap.ErrorLevel {
					errorLogCount++
				}
			}

			// We should have at least one error log per validation error
			assert.Assert(t, errorLogCount >= len(tt.validationErrors),
				"Expected at least %d error logs, got %d", len(tt.validationErrors), errorLogCount)
		})
	}
}
