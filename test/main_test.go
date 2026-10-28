//go:build e2e

package test

import (
	"fmt"
	"os"
	"testing"

	"github.com/tektoncd/pipelines-as-code/test/pkg/configfile"
)

func TestMain(m *testing.M) {
	if configPath := os.Getenv("PAC_E2E_CONFIG"); configPath != "" {
		if err := configfile.LoadConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading E2E config %s: %v\n", configPath, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}
