package codex

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
)

func TestWindowName_NamesARateLimitWindowByItsLength(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{seconds: 18000, want: "5h"},
		{seconds: 3600, want: "1h"},
		{seconds: 604800, want: "week"},
		{seconds: 86400, want: "1d"},
		{seconds: 2592000, want: "30d"},
		{seconds: 5400, want: "90m"},
		{seconds: 0, want: "limit"},
		{seconds: -1, want: "limit"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%d seconds", test.seconds), func(t *testing.T) {
			require.Equal(t, test.want, windowName(test.seconds))
		})
	}
}

func TestRepositoryAccountEnv_LeavesTheVariableUnsetOnlyForTheDefaultHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := &repository{}

	require.Equal(t, core.ProviderEnv{"CODEX_HOME": ""}, repo.AccountEnv(filepath.Join(home, ".codex")))
	require.Equal(t,
		core.ProviderEnv{"CODEX_HOME": filepath.Join(home, ".codex-work")},
		repo.AccountEnv(filepath.Join(home, ".codex-work")+"/"),
	)
}

func TestRepositoryAccountDir_ReturnsTheCleanedHomeTheEnvSelects(t *testing.T) {
	repo := &repository{}

	dir, err := repo.AccountDir(core.ProviderEnv{"CODEX_HOME": "/x/y/"})

	require.NoError(t, err)
	require.Equal(t, "/x/y", dir)
}

// unsignedJWT builds an access token whose payload expires at exp; Rig reads
// the expiry without verifying the signature.
func unsignedJWT(exp time.Time) string {
	encode := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return encode(`{"alg":"none"}`) + "." + encode(fmt.Sprintf(`{"exp":%d}`, exp.Unix())) + ".signature"
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

// writeAuth stores auth as the auth.json of a new Codex home, returning the
// env that selects the home. Empty auth leaves the home without a login.
func writeAuth(t *testing.T, auth string) core.ProviderEnv {
	t.Helper()
	home := t.TempDir()
	if auth != "" {
		require.NoError(t, os.WriteFile(filepath.Join(home, authFileName), []byte(auth), 0o600))
	}
	return core.ProviderEnv{"CODEX_HOME": home}
}

func tokenAuth(token string, accountID string) string {
	return fmt.Sprintf(`{"tokens":{"access_token":%q,"account_id":%q}}`, token, accountID)
}

const plusUsage = `{"plan_type":"plus","rate_limit":{` +
	`"primary_window":{"used_percent":12.5,"limit_window_seconds":18000,"reset_at":1792000000},` +
	`"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1792500000}}}`

func TestRepositoryAccountUsage_ReadsTheRateLimitsWithTheStoredLogin(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, plusUsage)
	token := unsignedJWT(time.Now().Add(time.Hour))
	env := writeAuth(t, tokenAuth(token, "acct-1"))
	repo := &repository{usageURL: server.URL}

	usage, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), usage.ReportedAt, time.Minute)
	usage.ReportedAt = time.Time{}
	require.Equal(t, &core.AccountUsage{
		Plan: "plus",
		Limits: []core.UsageLimit{
			{Name: "5h", UsedPercent: 12.5, ResetsAt: time.Unix(1792000000, 0).UTC()},
			{Name: "week", UsedPercent: 0},
		},
	}, usage, "an unused window has not started, so it has no reset")
	require.EqualValues(t, 1, server.calls.Load())
	headers := server.headers.Load()
	require.Equal(t, "Bearer "+token, headers.Get("Authorization"))
	require.Equal(t, "acct-1", headers.Get("ChatGPT-Account-Id"))
}

func TestRepositoryAccountUsage_OmitsTheAccountIDHeaderWhenTheLoginHasNone(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, plusUsage)
	env := writeAuth(t, `{"tokens":{"access_token":"opaque-token"}}`)
	repo := &repository{usageURL: server.URL}

	_, err := repo.AccountUsage(t.Context(), env)

	require.NoError(t, err)
	headers := server.headers.Load()
	require.Equal(t, "Bearer opaque-token", headers.Get("Authorization"), "a token that is no JWT is sent as is")
	require.Empty(t, headers.Values("ChatGPT-Account-Id"))
}

func TestRepositoryAccountUsage_SkipsWindowsTheEndpointReportsAsNull(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []core.UsageLimit
	}{
		{
			name: "a null secondary window",
			body: `{"plan_type":"free","rate_limit":{` +
				`"primary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_at":1792000000},` +
				`"secondary_window":null}}`,
			want: []core.UsageLimit{{Name: "week", UsedPercent: 3, ResetsAt: time.Unix(1792000000, 0).UTC()}},
		},
		{
			name: "a null primary window",
			body: `{"plan_type":"free","rate_limit":{"primary_window":null,` +
				`"secondary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_at":1792000000}}}`,
			want: []core.UsageLimit{{Name: "5h", UsedPercent: 7, ResetsAt: time.Unix(1792000000, 0).UTC()}},
		},
		{name: "both windows null", body: `{"plan_type":"free","rate_limit":{}}`},
		{name: "a null rate limit", body: `{"plan_type":"free","rate_limit":null}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newUsageServer(t, http.StatusOK, test.body)
			env := writeAuth(t, tokenAuth(unsignedJWT(time.Now().Add(time.Hour)), "acct-1"))
			repo := &repository{usageURL: server.URL}

			usage, err := repo.AccountUsage(t.Context(), env)

			require.NoError(t, err)
			require.Equal(t, "free", usage.Plan)
			require.Equal(t, test.want, usage.Limits)
		})
	}
}

func TestRepositoryAccountUsage_ReportsALoginItCannotUse(t *testing.T) {
	tests := []struct {
		name    string
		auth    string
		wantErr string
	}{
		{name: "no auth.json", wantErr: "not logged in"},
		{name: "an api key only", auth: `{"OPENAI_API_KEY":"sk-x","tokens":null}`, wantErr: "API key"},
		{name: "no tokens and no key", auth: `{"OPENAI_API_KEY":null,"tokens":null}`, wantErr: "not logged in"},
		{name: "an empty access token", auth: `{"tokens":{"access_token":" "}}`, wantErr: "not logged in"},
		{name: "an expired token", auth: tokenAuth(unsignedJWT(time.Now().Add(-time.Minute)), "a"), wantErr: "expired"},
		{name: "an auth.json that is not json", auth: `{oops`, wantErr: "decode codex login"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newUsageServer(t, http.StatusOK, plusUsage)
			env := writeAuth(t, test.auth)
			repo := &repository{usageURL: server.URL}

			usage, err := repo.AccountUsage(t.Context(), env)

			require.ErrorContains(t, err, test.wantErr)
			require.Nil(t, usage)
			require.EqualValues(t, 0, server.calls.Load(), "the endpoint is not asked without a usable login")
		})
	}
}

func TestRepositoryAccountUsage_ReportsWhenTheEndpointRefusesTheLogin(t *testing.T) {
	server := newUsageServer(t, http.StatusUnauthorized, `{}`)
	env := writeAuth(t, tokenAuth(unsignedJWT(time.Now().Add(time.Hour)), "acct-1"))
	repo := &repository{usageURL: server.URL}

	_, err := repo.AccountUsage(t.Context(), env)

	require.ErrorContains(t, err, "login was not accepted")
}

func TestRepositoryAccountUsage_AsksTheEndpointOncePerHomeWhileTheReadingIsCached(t *testing.T) {
	server := newUsageServer(t, http.StatusOK, plusUsage)
	auth := tokenAuth(unsignedJWT(time.Now().Add(time.Hour)), "acct-1")
	work := writeAuth(t, auth)
	personal := writeAuth(t, auth)
	repo := &repository{usageURL: server.URL, usage: providerkit.NewUsageCache()}

	first, err := repo.AccountUsage(t.Context(), work)
	require.NoError(t, err)
	again, err := repo.AccountUsage(t.Context(), work)
	require.NoError(t, err)
	_, err = repo.AccountUsage(t.Context(), personal)
	require.NoError(t, err)

	require.Same(t, first, again)
	require.EqualValues(t, 2, server.calls.Load())
}
