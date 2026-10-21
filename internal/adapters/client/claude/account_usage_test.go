package claude

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

func TestKeychainService_NamesTheKeychainItemOfTheAccountBySettingHash(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		want    string
	}{
		{name: "the default account", setting: "", want: "Claude Code-credentials"},
		{
			name:    "a configuration directory adds the first eight hex digits of its sha256",
			setting: "/home/me/.claude-work",
			want:    "Claude Code-credentials-95e632f0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, keychainService(test.setting))
		})
	}
}

func TestKeychainService_HashesTheSettingAsWritten(t *testing.T) {
	require.NotEqual(t, keychainService("/home/me/.claude-work"), keychainService("/home/me/.claude-work/"))
}

func TestRepositoryAccountEnv_LeavesTheVariableUnsetOnlyForTheDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := &repository{}

	require.Equal(t, core.ProviderEnv{"CLAUDE_CONFIG_DIR": ""}, repo.AccountEnv(filepath.Join(home, ".claude")))
	require.Equal(t, core.ProviderEnv{"CLAUDE_CONFIG_DIR": ""}, repo.AccountEnv(filepath.Join(home, ".claude")+"/"))
	require.Equal(t,
		core.ProviderEnv{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-work")},
		repo.AccountEnv(filepath.Join(home, ".claude-work")),
	)
}

func TestRepositoryAccountDir_ReturnsTheCleanedDirTheEnvSelects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := &repository{}

	dir, err := repo.AccountDir(core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/x/y/"})
	require.NoError(t, err)
	require.Equal(t, "/x/y", dir)

	dir, err = repo.AccountDir(core.ProviderEnv{"CLAUDE_CONFIG_DIR": ""})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".claude"), dir, "an unset variable means the default dir")
}

// usageServer answers every request with body and counts and remembers them.
type usageServer struct {
	*httptest.Server
	calls   atomic.Int32
	headers atomic.Pointer[http.Header]
}

func newUsageServer(t *testing.T, status int, body string) *usageServer {
	t.Helper()
	server := &usageServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.calls.Add(1)
		headers := r.Header.Clone()
		server.headers.Store(&headers)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// newUsageRepository returns a repository that reads usage from usageURL, the
// env that selects a fresh account dir, and that dir. The keychain holds no
// login, so a login is only found in the dir's credentials file.
func newUsageRepository(t *testing.T, usageURL string) (*repository, core.ProviderEnv, string) {
	t.Helper()
	dir := t.TempDir()
	env := core.ProviderEnv{"CLAUDE_CONFIG_DIR": dir}
	runner := subprocess.NewMockRunner(t)
	// The keychain is only asked on macOS.
	runner.EXPECT().
		Run(mock.Anything, "", "security", "find-generic-password", "-s", keychainService(dir), "-w").
		Return(subprocess.Result{}, errors.New("the specified item could not be found")).
		Maybe()
	return &repository{runner: runner, usageURL: usageURL}, env, dir
}

func writeCredentials(t *testing.T, dir string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, credentialsFileName), []byte(content), 0o600))
}

func credentialsJSON(expiresAt time.Time, subscription string) string {
	return fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok","expiresAt":%d,"subscriptionType":%q}}`,
		expiresAt.UnixMilli(), subscription)
}

func TestRepositoryAccountUsage_ReadsTheNewerLimitsAnswerWithTheStoredLogin(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, `{"limits":[`+
		`{"kind":"session","percent":42,"resets_at":"2026-10-13T17:09:59.895009+00:00"},`+
		`{"kind":"weekly_all","percent":23,"resets_at":"2026-10-19T16:59:59+00:00"},`+
		`{"kind":"weekly_scoped","percent":15,"resets_at":null,"scope":{"model":{"display_name":"Fable"}}}`+
		`],"five_hour":{"utilization":99}}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	writeCredentials(t, dir, credentialsJSON(time.Now().Add(time.Hour), "max"))

	usage, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), usage.ReportedAt, time.Minute)
	usage.ReportedAt = time.Time{}
	require.Equal(t, &core.AccountUsage{
		Plan: "max",
		Limits: []core.UsageLimit{
			{Name: "5h", UsedPercent: 42, ResetsAt: time.Date(2026, time.October, 13, 17, 9, 59, 895009000, time.UTC)},
			{Name: "week", UsedPercent: 23, ResetsAt: time.Date(2026, time.October, 19, 16, 59, 59, 0, time.UTC)},
			{Name: "week fable", UsedPercent: 15},
		},
	}, usage, "the limits win over the older five_hour window")
	require.EqualValues(t, 1, server.calls.Load())
	headers := server.headers.Load()
	require.Equal(t, "Bearer tok", headers.Get("Authorization"))
	require.Equal(t, "oauth-2025-04-20", headers.Get("anthropic-beta"))
}

func TestRepositoryAccountUsage_ReadsTheOlderNamedWindowsAnswer(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, `{`+
		`"five_hour":{"utilization":5,"resets_at":"2026-10-13T17:09:59.895009+00:00"},`+
		`"seven_day":{"utilization":23,"resets_at":"2026-10-19T16:59:59+00:00"},`+
		`"seven_day_opus":null}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	writeCredentials(t, dir, credentialsJSON(time.Now().Add(time.Hour), "pro"))

	usage, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	require.Equal(t, "pro", usage.Plan)
	require.Equal(t, []core.UsageLimit{
		{Name: "5h", UsedPercent: 5, ResetsAt: time.Date(2026, time.October, 13, 17, 9, 59, 895009000, time.UTC)},
		{Name: "week", UsedPercent: 23, ResetsAt: time.Date(2026, time.October, 19, 16, 59, 59, 0, time.UTC)},
	}, usage.Limits)
}

func TestRepositoryAccountUsage_AcceptsAHexEncodedLogin(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, `{"five_hour":{"utilization":5}}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	writeCredentials(t, dir, hex.EncodeToString([]byte(credentialsJSON(time.Now().Add(time.Hour), "max")))+"\n")

	usage, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	require.Equal(t, "max", usage.Plan)
	require.Equal(t, "Bearer tok", server.headers.Load().Get("Authorization"))
}

func TestRepositoryAccountUsage_ReportsAnExpiredLoginWithoutAskingTheEndpoint(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, `{}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	writeCredentials(t, dir, credentialsJSON(time.Now().Add(-time.Minute), "max"))

	usage, err := repo.AccountUsage(t.Context(), env)

	require.ErrorContains(t, err, "expired")
	require.Nil(t, usage)
	require.EqualValues(t, 0, server.calls.Load())
}

func TestRepositoryAccountUsage_ReportsALoginThatIsMissingOrNotASubscriptionLogin(t *testing.T) {
	tests := []struct {
		name        string
		credentials string
		stored      bool
		wantErr     string
	}{
		{name: "no credentials anywhere", wantErr: "not logged in"},
		{name: "an empty credentials file", stored: true, credentials: " \n", wantErr: "not logged in"},
		{
			name:        "credentials without a subscription login",
			stored:      true,
			credentials: `{}`,
			wantErr:     "not logged in with",
		},
		{
			name:        "a subscription login without a token",
			stored:      true,
			credentials: `{"claudeAiOauth":{"accessToken":" "}}`,
			wantErr:     "not logged in with",
		},
		{name: "credentials that are not json", stored: true, credentials: `{oops`, wantErr: "decode claude login"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newUsageServer(t, http.StatusOK, `{}`)
			repo, env, dir := newUsageRepository(t, server.URL)
			if test.stored {
				writeCredentials(t, dir, test.credentials)
			}

			usage, err := repo.AccountUsage(t.Context(), env)

			require.ErrorContains(t, err, test.wantErr)
			require.Nil(t, usage)
			require.EqualValues(t, 0, server.calls.Load())
		})
	}
}

func TestRepositoryAccountUsage_ReportsWhenTheEndpointRefusesTheLogin(t *testing.T) {
	server := newUsageServer(t, http.StatusUnauthorized, `{}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	writeCredentials(t, dir, credentialsJSON(time.Now().Add(time.Hour), "max"))

	_, err := repo.AccountUsage(t.Context(), env)

	require.ErrorContains(t, err, "login was not accepted")
}

func TestRepositoryAccountUsage_AsksTheEndpointOnceWhileTheReadingIsCached(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, `{"five_hour":{"utilization":5}}`)
	repo, env, dir := newUsageRepository(t, server.URL)
	repo.usage = providerkit.NewUsageCache()
	writeCredentials(t, dir, credentialsJSON(time.Now().Add(time.Hour), "max"))

	first, err := repo.AccountUsage(t.Context(), env)
	require.NoError(t, err)
	second, err := repo.AccountUsage(t.Context(), env)
	require.NoError(t, err)

	require.Same(t, first, second)
	require.EqualValues(t, 1, server.calls.Load())
}

func TestRepositoryAccountUsage_PrefersTheKeychainLoginOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Claude Code keeps its login in the keychain on macOS only")
	}
	server := newUsageServer(t, http.StatusOK, `{"five_hour":{"utilization":5}}`)
	dir := t.TempDir()
	env := core.ProviderEnv{"CLAUDE_CONFIG_DIR": dir}
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "security", "find-generic-password", "-s", keychainService(dir), "-w").
		Return(subprocess.Result{Stdout: credentialsJSON(time.Now().Add(time.Hour), "max") + "\n"}, nil).
		Once()
	repo := &repository{runner: runner, usageURL: server.URL}
	writeCredentials(t, dir, `{"claudeAiOauth":{"accessToken":"file-token"}}`)

	usage, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	require.Equal(t, "max", usage.Plan)
	require.Equal(t, "Bearer tok", server.headers.Load().Get("Authorization"))
}
