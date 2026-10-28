package setup

import (
	"fmt"
	"os"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

// PacOptsWithAPIRetry returns the default PAC options with API retries enabled.
func PacOptsWithAPIRetry() *info.PacOpts {
	pacOpts := info.NewPacOpts()
	pacOpts.EnableAPIRetry = true
	return pacOpts
}

// RequireEnvs checks if all required environment variables are set.
// It takes a variadic list of environment variable keys and returns an error
// if any of the required variables are not set.
func RequireEnvs(keys ...string) error {
	missingEnvs := []string{}
	for _, key := range keys {
		_, err := GetRequiredEnv(key)
		if err != nil {
			missingEnvs = append(missingEnvs, key)
		}
	}
	if len(missingEnvs) > 0 {
		return fmt.Errorf("missing required env variable(s): %v, cannot continue", missingEnvs)
	}
	return nil
}

// GetRequiredEnv retrieves the value of a required environment variable.
// If the environment variable is not set or is empty, it returns an error.
func GetRequiredEnv(key string) (string, error) {
	env := os.Getenv(key)
	if env == "" {
		return "", fmt.Errorf("missing required env variable: \"%s\", cannot continue", key)
	}
	return env, nil
}
