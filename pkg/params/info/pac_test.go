package info

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/params/settings"
	"gotest.tools/v3/assert"
)

func TestNewPacOpts(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "default settings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := NewPacOpts()

			assert.Equal(t, settings.PACApplicationNameDefaultValue, opts.ApplicationName)
			value, ok := opts.HubCatalogs.Load("default")
			assert.Assert(t, ok)
			catalog, ok := value.(settings.HubCatalog)
			assert.Assert(t, ok)
			assert.Equal(t, "default", catalog.Index)
			assert.Equal(t, settings.ArtifactHubURLDefaultValue, catalog.URL)
		})
	}
}

func TestPacOptsDeepCopy(t *testing.T) {
	tests := []struct {
		name string
		opts *PacOpts
	}{
		{
			name: "copies fields",
			opts: &PacOpts{
				Settings: settings.Settings{
					ApplicationName:    "custom app",
					TektonDashboardURL: "https://dashboard.example.test",
					SecretAutoCreation: true,
				},
				WebhookType:        "github",
				PayloadFile:        "payload.json",
				TektonDashboardURL: "https://legacy-dashboard.example.test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &PacOpts{}

			tt.opts.DeepCopy(out)

			assert.Equal(t, tt.opts.ApplicationName, out.ApplicationName)
			assert.Equal(t, tt.opts.Settings.TektonDashboardURL, out.Settings.TektonDashboardURL)
			assert.Equal(t, tt.opts.SecretAutoCreation, out.SecretAutoCreation)
			assert.Equal(t, tt.opts.WebhookType, out.WebhookType)
			assert.Equal(t, tt.opts.PayloadFile, out.PayloadFile)
			assert.Equal(t, tt.opts.TektonDashboardURL, out.TektonDashboardURL)
		})
	}
}

func TestPacOptsAddFlags(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		defaults map[string]string
	}{
		{
			name: "registers flags with defaults from environment",
			env: map[string]string{
				"PAC_GIT_PROVIDER_TYPE":  "github",
				"PAC_PAYLOAD_FILE":       "payload.json",
				"PAC_APPLICATION_NAME":   "pac app",
				"PAC_SECRET_AUTO_CREATE": "",
			},
			defaults: map[string]string{
				"git-provider-type":    "github",
				"payload-file":         "payload.json",
				"application-name":     "pac app",
				"secret-auto-creation": "false",
			},
		},
		{
			name: "registers secret auto creation truthy default",
			env: map[string]string{
				"PAC_GIT_PROVIDER_TYPE":  "",
				"PAC_PAYLOAD_FILE":       "",
				"PAC_APPLICATION_NAME":   "",
				"PAC_SECRET_AUTO_CREATE": "yes",
			},
			defaults: map[string]string{
				"git-provider-type":    "",
				"payload-file":         "",
				"application-name":     "",
				"secret-auto-creation": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			opts := NewPacOpts()
			cmd := &cobra.Command{}

			err := opts.AddFlags(cmd)
			assert.NilError(t, err)

			for name, want := range tt.defaults {
				flag := cmd.Flags().Lookup(name)
				if flag == nil {
					flag = cmd.PersistentFlags().Lookup(name)
				}
				assert.Assert(t, flag != nil, "expected flag %q to be registered", name)
				assert.Equal(t, want, flag.DefValue)
			}
		})
	}
}
