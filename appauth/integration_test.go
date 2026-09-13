package appauth_test

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/LarsArtmann/go-github-kit/appauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration smoke against the real GitHub API. Skipped unless
// GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY are set; no test in this file
// ever creates or mutates anything on GitHub (reads and token mints
// only). GITHUB_APP_INSTALLATION_ID optionally exercises the full
// JWT → installation-token path.
func TestIntegration_AppAuthAgainstRealGitHub(t *testing.T) {
	appID := os.Getenv("GITHUB_APP_ID")
	pemPath := os.Getenv("GITHUB_APP_PRIVATE_KEY")
	if appID == "" || pemPath == "" {
		t.Skip("GITHUB_APP_ID / GITHUB_APP_PRIVATE_KEY not set; skipping live smoke")
	}

	appIDInt, err := strconv.ParseInt(appID, 10, 64)
	require.NoError(t, err, "GITHUB_APP_ID must be numeric")

	pemKey, err := os.ReadFile(pemPath)
	require.NoError(t, err, "GITHUB_APP_PRIVATE_KEY must point at the downloaded PEM")

	app, err := appauth.NewAppAuth(appIDInt, pemKey)
	require.NoError(t, err)

	jwt, err := app.Sign(time.Now())
	require.NoError(t, err)
	assert.Greater(t, len(jwt), 100, "signed JWT looks like a JWT")

	appsClient, err := appauth.NewAppsClient(app, "")
	require.NoError(t, err)

	installations, _, err := appsClient.Apps.ListInstallations(t.Context(), nil)
	require.NoError(t, err, "JWT must be accepted by the real Apps API")

	instID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	if instID == "" {
		if len(installations) == 1 {
			instID = strconv.FormatInt(installations[0].GetID(), 10)
		} else {
			t.Skipf(
				"GITHUB_APP_INSTALLATION_ID not set and %d installations found; skipping token mint",
				len(installations),
			)
		}
	}

	installationID, err := strconv.ParseInt(instID, 10, 64)
	require.NoError(t, err, "installation id must be numeric")

	source, err := appauth.NewInstallationTokenSource(installationID, appsClient.Apps)
	require.NoError(t, err)

	token, err := source.Token()
	require.NoError(t, err, "installation token mint against the real API")
	assert.NotEmpty(t, token)

	cached, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, token, cached, "second call must be served from cache")
}
