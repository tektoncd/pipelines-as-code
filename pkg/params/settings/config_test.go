package settings

import (
	"reflect"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
)

func TestSyncConfig(t *testing.T) {
	logger, _ := logger.GetLogger()

	testCases := []struct {
		name           string
		configMap      map[string]string
		expectedStruct Settings
		expectedError  string
	}{
		{
			name:      "With all default values",
			configMap: map[string]string{},
			expectedStruct: Settings{
				ApplicationName:                      "Pipelines as Code CI",
				HubCatalogs:                          nil,
				RemoteTasks:                          true,
				MaxKeepRunsUpperLimit:                0,
				DefaultMaxKeepRuns:                   0,
				BitbucketCloudCheckSourceIP:          true,
				BitbucketCloudAdditionalSourceIP:     "",
				TektonDashboardURL:                   "",
				AutoConfigureNewGitHubRepo:           false,
				AutoConfigureRepoNamespaceTemplate:   "",
				SecretAutoCreation:                   true,
				SecretGHAppRepoScoped:                true,
				SecretGhAppTokenScopedExtraRepos:     "",
				ErrorLogSnippet:                      true,
				ErrorLogSnippetNumberOfLines:         3,
				ErrorDetection:                       true,
				ErrorDetectionNumberOfLines:          50,
				ErrorDetectionSimpleRegexp:           "^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+)?([ ]*)?(?P<error>.*)",
				EnableCancelInProgressOnPullRequests: false,
				EnableCancelInProgressOnPush:         false,
				SkipPushEventForPRCommits:            true,
				CustomConsoleName:                    "",
				CustomConsoleURL:                     "",
				CustomConsolePRdetail:                "",
				CustomConsolePRTaskLog:               "",
				CustomConsoleNamespaceURL:            "",
				RememberOKToTest:                     false,
				APIRetryMaxAttempts:                  4,
				APIRetryMaxWaitSeconds:               120,
			},
		},
		{
			name: "override values",
			configMap: map[string]string{
				"application-name":                        "pac-pac",
				"remote-tasks":                            "false",
				"max-keep-run-upper-limit":                "10",
				"default-max-keep-runs":                   "5",
				"bitbucket-cloud-check-source-ip":         "false",
				"bitbucket-cloud-additional-source-ip":    "some-ip",
				"tekton-dashboard-url":                    "https://tekton-dashboard",
				"auto-configure-new-github-repo":          "true",
				"auto-configure-repo-namespace-template":  "template",
				"auto-configure-repo-repository-template": "template",
				"secret-auto-create":                      "false",
				"secret-github-app-token-scoped":          "false",
				"secret-github-app-scope-extra-repos":     "extra-repos",
				"error-log-snippet":                       "false",
				"error-detection-from-container-logs":     "false",
				"error-detection-max-number-of-lines":     "100",
				"error-detection-simple-regexp":           "^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+)?([ ]*)?(?P<error>.*)",
				"custom-console-name":                     "custom-console",
				"custom-console-url":                      "https://custom-console",
				"custom-console-url-pr-details":           "https://custom-console-pr-details",
				"custom-console-url-pr-tasklog":           "https://custom-console-pr-tasklog",
				"custom-console-url-namespace":            "https://custom-console-namespace",
				"remember-ok-to-test":                     "false",
				"skip-push-event-for-pr-commits":          "true",
				"require-ok-to-test-sha":                  "true",
			},
			expectedStruct: Settings{
				ApplicationName:                      "pac-pac",
				HubCatalogs:                          nil,
				RemoteTasks:                          false,
				MaxKeepRunsUpperLimit:                10,
				DefaultMaxKeepRuns:                   5,
				BitbucketCloudCheckSourceIP:          false,
				BitbucketCloudAdditionalSourceIP:     "some-ip",
				TektonDashboardURL:                   "https://tekton-dashboard",
				AutoConfigureNewGitHubRepo:           true,
				AutoConfigureRepoNamespaceTemplate:   "template",
				AutoConfigureRepoRepositoryTemplate:  "template",
				SecretAutoCreation:                   false,
				SecretGHAppRepoScoped:                false,
				SecretGhAppTokenScopedExtraRepos:     "extra-repos",
				ErrorLogSnippet:                      false,
				ErrorLogSnippetNumberOfLines:         3,
				ErrorDetection:                       false,
				ErrorDetectionNumberOfLines:          100,
				ErrorDetectionSimpleRegexp:           "^(?P<filename>[^:]*):(?P<line>[0-9]+):(?P<column>[0-9]+)?([ ]*)?(?P<error>.*)",
				EnableCancelInProgressOnPullRequests: false,
				EnableCancelInProgressOnPush:         false,
				SkipPushEventForPRCommits:            true,
				CustomConsoleName:                    "custom-console",
				CustomConsoleURL:                     "https://custom-console",
				CustomConsolePRdetail:                "https://custom-console-pr-details",
				CustomConsolePRTaskLog:               "https://custom-console-pr-tasklog",
				CustomConsoleNamespaceURL:            "https://custom-console-namespace",
				RememberOKToTest:                     false,
				RequireOkToTestSHA:                   true,
				APIRetryMaxAttempts:                  4,
				APIRetryMaxWaitSeconds:               120,
			},
		},
		{
			name: "invalid value for bool field",
			configMap: map[string]string{
				"remote-tasks": "invalid",
			},
			expectedError: "invalid value for bool field RemoteTasks: strconv.ParseBool: parsing \"invalid\": invalid syntax",
		},
		{
			name: "invalid value for int field",
			configMap: map[string]string{
				"max-keep-run-upper-limit": "invalid",
			},
			expectedError: "invalid value for int field MaxKeepRunsUpperLimit: strconv.ParseInt: parsing \"invalid\": invalid syntax",
		},
		{
			name: "invalid value regex",
			configMap: map[string]string{
				"error-detection-simple-regexp": "[",
			},
			expectedError: "custom validation failed for field ErrorDetectionSimpleRegexp: invalid regex: error parsing regexp: missing closing ]: `[`",
		},
		{
			name: "invalid value url",
			configMap: map[string]string{
				"tekton-dashboard-url": "invalid-url",
			},
			expectedError: "custom validation failed for field TektonDashboardURL: invalid value for URL, error: parse \"invalid-url\": invalid URI for request",
		},
		{
			name: "invalid value url for custom console pr detail",
			configMap: map[string]string{
				"custom-console-url-pr-tasklog": "invalid-url",
			},
			expectedError: "custom validation failed for field CustomConsolePRTaskLog: invalid value, must start with http:// or https://",
		},
		{
			name: "invalid value trusted provider hostnames",
			configMap: map[string]string{
				"trusted-provider-hostnames": "ghe.example.com, https://user@bad.example.com",
			},
			expectedError: "custom validation failed for field TrustedProviderHostnames: invalid value for trusted-provider-hostnames",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var test Settings

			err := SyncConfig(logger, &test, tc.configMap, DefaultValidators())

			// set hub catalogs to nil to avoid comparison error
			// test separately
			test.HubCatalogs = nil

			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				return
			}
			assert.NilError(t, err)

			if !reflect.DeepEqual(test, tc.expectedStruct) {
				t.Errorf("failure, actual and expected struct:\nActual: %#v\nExpected: %#v", test, tc.expectedStruct)
			}
		})
	}
}

func TestIsValidTrustedProviderHostnames(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:  "empty value means not configured",
			value: "  ",
		},
		{
			name:  "valid comma separated hostnames",
			value: "ghe.example.com, gitlab.example.com",
		},
		{
			name:  "https url spelling of a hostname",
			value: "https://ghe.example.com",
		},
		{
			name:    "hostname with a path is refused",
			value:   "ghe.example.com/api/v3",
			wantErr: "invalid value for trusted-provider-hostnames",
		},
		{
			name:    "hostname with credentials is refused",
			value:   "https://user@ghe.example.com",
			wantErr: "invalid value for trusted-provider-hostnames",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := isValidTrustedProviderHostnames(tt.value)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestDefaultSettings(t *testing.T) {
	settings := DefaultSettings()
	assert.Equal(t, settings.ApplicationName, "Pipelines as Code CI")

	catalogValue, ok := settings.HubCatalogs.Load("default")
	assert.Assert(t, ok)
	catalog, ok := catalogValue.(HubCatalog)
	assert.Assert(t, ok)
	assert.Equal(t, catalog.Index, "default")
}
