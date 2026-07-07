package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	rotationThreshold     = 7 * 24 * time.Hour
	rotationNewExpiry     = 30 * 24 * time.Hour
	introspectionCacheTTL = 1 * time.Hour
)

// introspectionCacheEntry caches the result of a token introspection so the
// GitLab API is not hit on every webhook event or watcher reconciliation loop.
type introspectionCacheEntry struct {
	tokenHash string
	expiresAt *time.Time
	checkedAt time.Time
}

var (
	errTokenRotatedSecretUpdateFailed = errors.New("token rotated but secret update failed")
	tokenRotationLocks                sync.Map
	// tokenIntrospectionCache is keyed by "namespace/name" of the Repository CR.
	tokenIntrospectionCache sync.Map
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// cachedIntrospectionFresh reports whether a cached introspection result for
// repoKey is still valid for the given token: the entry must be younger than
// introspectionCacheTTL, match the current token (so a manually updated
// secret invalidates the cache), and the cached expiry must not be within the
// rotation threshold.
func cachedIntrospectionFresh(repoKey, tokenHash string) bool {
	val, ok := tokenIntrospectionCache.Load(repoKey)
	if !ok {
		return false
	}
	entry, ok := val.(introspectionCacheEntry)
	if !ok || entry.tokenHash != tokenHash {
		return false
	}
	if time.Since(entry.checkedAt) > introspectionCacheTTL {
		return false
	}
	// Non-expiring token or expiry still far enough away.
	return entry.expiresAt == nil || time.Until(*entry.expiresAt) >= rotationThreshold
}

func cacheIntrospection(repoKey, tokenHash string, pat *gitlab.PersonalAccessToken) {
	entry := introspectionCacheEntry{tokenHash: tokenHash, checkedAt: time.Now()}
	if pat.ExpiresAt != nil {
		expiresAt := time.Time(*pat.ExpiresAt)
		entry.expiresAt = &expiresAt
	}
	tokenIntrospectionCache.Store(repoKey, entry)
}

func getRepositoryLock(repoKey string) *sync.Mutex {
	val, _ := tokenRotationLocks.LoadOrStore(repoKey, &sync.Mutex{})
	mu, ok := val.(*sync.Mutex)
	if !ok {
		return &sync.Mutex{}
	}
	return mu
}

func (v *Provider) hasGitProviderSecret() bool {
	return v.repo != nil && v.repo.Spec.GitProvider != nil &&
		v.repo.Spec.GitProvider.Secret != nil && v.repo.Spec.GitProvider.Secret.Name != ""
}

func (v *Provider) isTokenAutoRotationEnabled() bool {
	if !v.hasGitProviderSecret() {
		return false
	}
	if v.repo.Spec.Settings == nil || v.repo.Spec.Settings.Gitlab == nil {
		return false
	}
	if v.repo.Spec.Settings.Gitlab.TokenAutoRotation == nil {
		return false
	}
	return *v.repo.Spec.Settings.Gitlab.TokenAutoRotation
}

func (v *Provider) introspectToken() (*gitlab.PersonalAccessToken, error) {
	pat, resp, err := v.Client().PersonalAccessTokens.GetSinglePersonalAccessToken()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("token is invalid or expired")
		}
		return nil, fmt.Errorf("introspect token: %w", err)
	}
	return pat, nil
}

func needsRotation(pat *gitlab.PersonalAccessToken) bool {
	if pat.ExpiresAt == nil {
		return false
	}
	if !pat.Active {
		return false
	}
	expiresAt := time.Time(*pat.ExpiresAt)
	return time.Until(expiresAt) < rotationThreshold
}

var errMissingSelfRotateScope = fmt.Errorf("token lacks 'api' or 'self_rotate' scope required for auto-rotation — disable with spec.settings.gitlab.token_auto_rotation: false")

func (v *Provider) rotateToken() (*gitlab.PersonalAccessToken, error) {
	newExpiry := gitlab.ISOTime(time.Now().Add(rotationNewExpiry))
	opts := &gitlab.RotatePersonalAccessTokenOptions{
		ExpiresAt: &newExpiry,
	}

	pat, resp, err := v.Client().PersonalAccessTokens.RotatePersonalAccessTokenSelf(opts)
	if err == nil {
		return pat, nil
	}

	if resp != nil && resp.StatusCode == http.StatusForbidden {
		return nil, errMissingSelfRotateScope
	}

	// If PAT self-rotate fails with other 4xx, try project access token self-rotate.
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && v.targetProjectID != 0 {
		v.Logger.Debugf("PAT self-rotate returned %d, trying project access token rotation for project %d", resp.StatusCode, v.targetProjectID)
		projectOpts := &gitlab.RotateProjectAccessTokenOptions{
			ExpiresAt: &newExpiry,
		}
		projectPat, projectResp, projectErr := v.Client().ProjectAccessTokens.RotateProjectAccessTokenSelf(v.targetProjectID, projectOpts)
		if projectErr == nil {
			return &projectPat.PersonalAccessToken, nil
		}
		if projectResp != nil && projectResp.StatusCode == http.StatusForbidden {
			return nil, errMissingSelfRotateScope
		}
		return nil, fmt.Errorf("PAT rotation failed (%w), project token rotation also failed: %w", err, projectErr)
	}

	return nil, fmt.Errorf("rotate token: %w", err)
}

// verifySecretWriteAccess checks, via a server-side dry-run update with
// unchanged data, that the controller can actually write the git provider
// Secret. It is called before rotating the token so that a permission
// failure aborts the rotation while the old token is still valid — once
// rotated, the old token is revoked and a failed secret update would lose
// the new token irrecoverably.
func (v *Provider) verifySecretWriteAccess(ctx context.Context) error {
	secretName := v.repo.Spec.GitProvider.Secret.Name
	secretNS := v.repo.GetNamespace()
	secret, err := v.run.Clients.Kube.CoreV1().Secrets(secretNS).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	_, err = v.run.Clients.Kube.CoreV1().Secrets(secretNS).Update(ctx, secret, metav1.UpdateOptions{
		DryRun: []string{metav1.DryRunAll},
	})
	return err
}

func (v *Provider) updateKubeSecret(ctx context.Context, newToken string) error {
	if !v.hasGitProviderSecret() {
		return fmt.Errorf("repository CR has no git_provider.secret configured")
	}

	secretName := v.repo.Spec.GitProvider.Secret.Name
	secretKey := v.repo.Spec.GitProvider.Secret.Key
	if secretKey == "" {
		secretKey = secrets.DefaultGitProviderSecretKey
	}
	secretNS := v.repo.GetNamespace()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := v.run.Clients.Kube.CoreV1().Secrets(secretNS).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[secretKey] = []byte(newToken)
		_, err = v.run.Clients.Kube.CoreV1().Secrets(secretNS).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func (v *Provider) maybeRotateToken(ctx context.Context) (string, error) {
	if !v.hasGitProviderSecret() {
		return "", fmt.Errorf("repository CR has no git_provider.secret configured")
	}

	repoKey := fmt.Sprintf("%s/%s", v.repo.Namespace, v.repo.Name)
	mu := getRepositoryLock(repoKey)
	mu.Lock()
	defer mu.Unlock()

	tokenHash := ""
	if v.Token != nil {
		tokenHash = hashToken(*v.Token)
	}

	if cachedIntrospectionFresh(repoKey, tokenHash) {
		return "", nil
	}

	pat, err := v.introspectToken()
	if err != nil {
		return "", fmt.Errorf("introspect: %w", err)
	}

	if !needsRotation(pat) {
		cacheIntrospection(repoKey, tokenHash, pat)
		return "", nil
	}

	expiresAt := time.Time(*pat.ExpiresAt)
	v.Logger.Infof("gitlab token expires at %s (within %v threshold), rotating", expiresAt.Format(time.RFC3339), rotationThreshold)

	// Verify we can write the secret before rotating: once rotated the old
	// token is revoked, so a permission failure must abort the rotation
	// while the old token is still valid.
	if err := v.verifySecretWriteAccess(ctx); err != nil {
		return "", fmt.Errorf("skipping token rotation, cannot update secret %s/%s (old token untouched): %w",
			v.repo.GetNamespace(), v.repo.Spec.GitProvider.Secret.Name, err)
	}

	newPat, err := v.rotateToken()
	if err != nil {
		return "", fmt.Errorf("rotate: %w", err)
	}

	if err := v.updateKubeSecret(ctx, newPat.Token); err != nil {
		v.Logger.Errorf("CRITICAL: gitlab token was rotated but failed to update kubernetes secret: %v — old token is revoked, manual intervention required", err)
		return "", fmt.Errorf("%w (old token revoked): %w", errTokenRotatedSecretUpdateFailed, err)
	}

	newExpiryStr := "unknown"
	if newPat.ExpiresAt != nil {
		newExpiryStr = time.Time(*newPat.ExpiresAt).Format(time.RFC3339)
	}
	v.eventEmitter.EmitMessage(v.repo, zap.InfoLevel, "GitLabTokenRotated",
		fmt.Sprintf("GitLab access token rotated, new expiry: %s", newExpiryStr))

	cacheIntrospection(repoKey, hashToken(newPat.Token), newPat)

	return newPat.Token, nil
}
