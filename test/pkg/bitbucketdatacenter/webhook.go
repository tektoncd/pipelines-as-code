package bitbucketdatacenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// GenerateHMAC returns the X-Hub-Signature value for payload signed with secret.
func GenerateHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SendWebhook posts payload to the event listener, setting X-Event-Key for the
// event type and X-Hub-Signature for the HMAC signature.
func SendWebhook(ctx context.Context, t *testing.T, elURL string, payload []byte, signature, eventType string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, elURL, bytes.NewBuffer(payload))
	assert.NilError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Key", eventType)
	if signature != "" {
		req.Header.Set("X-Hub-Signature", signature)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}
