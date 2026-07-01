package secrets

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	basicAuthGitConfigData = `
	[credential "%s"]
	helper=store
	`
	//nolint:gosec
	basicAuthSecretName       = `pac-gitauth-%s`
	bitbucketCloudGitUsername = "x-bitbucket-api-token-auth"
	ranStringSeedLen          = 6
)

// MakeBasicAuthSecret Make a secret for git-clone basic-auth workspace.
func MakeBasicAuthSecret(runevent *info.Event, secretName string) (*corev1.Secret, error) {
	// Bitbucket Data Center have a different Clone URL than it's Repository URL, so we
	// have to separate them
	cloneURL := runevent.URL
	if runevent.CloneURL != "" {
		cloneURL = runevent.CloneURL
	}

	repoURL, err := url.Parse(cloneURL)
	if err != nil {
		return nil, fmt.Errorf("cannot parse url %s: %w", cloneURL, err)
	}

	gitUser := provider.DefaultProviderAPIUser
	if runevent.Provider.User != "" {
		gitUser = runevent.Provider.User
	}
	if strings.EqualFold(repoURL.Hostname(), "bitbucket.org") {
		gitUser = bitbucketCloudGitUsername
	}

	// Bitbucket Data Center token have / into it, so unless we quote the URL them it's
	// impossible to use it🤡
	//
	// It supposed not working on GitHub according to
	// https://stackoverflow.com/a/24719496 but arguably GitHub have a better
	// product and would not do such things.
	//
	// maybe we could patch the git-clone task too but that probably be a pain
	// in the *** to do it in shell.
	token := url.QueryEscape(runevent.Provider.Token)

	baseCloneURL := fmt.Sprintf("%s://%s", repoURL.Scheme, repoURL.Host)
	urlWithToken := fmt.Sprintf("%s://%s:%s@%s%s", repoURL.Scheme, gitUser, token, repoURL.Host, repoURL.Path)
	secretData := map[string]string{
		".gitconfig":       fmt.Sprintf(basicAuthGitConfigData, baseCloneURL),
		".git-credentials": urlWithToken,
		// With the GitHub APP method the token is available for 8h if you have
		// the user to server token expiration.  the token is scoped to the
		// installation ID
		"git-provider-token": token,
	}
	annotations := map[string]string{
		"pipelinesascode.tekton.dev/url": cloneURL,
		keys.SHA:                         runevent.SHA,
		keys.URLOrg:                      runevent.Organization,
		keys.URLRepository:               runevent.Repository,
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by": pipelinesascode.GroupName,
		keys.URLOrg:                    formatting.CleanValueKubernetes(runevent.Organization),
		keys.URLRepository:             formatting.CleanValueKubernetes(runevent.Repository),
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Labels:      labels,
			Annotations: annotations,
		},
		StringData: secretData,
	}, nil
}

func GenerateBasicAuthSecretName() string {
	return strings.ToLower(
		fmt.Sprintf(basicAuthSecretName, random.AlphaString(ranStringSeedLen)),
	)
}
