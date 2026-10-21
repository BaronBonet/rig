package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
)

const (
	// defaultUsageURL is where Codex reads the rate limits of the logged-in
	// ChatGPT account.
	defaultUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	authFileName    = "auth.json"
)

// AccountDir returns the Codex home env makes Codex use.
func (r *repository) AccountDir(env core.ProviderEnv) (string, error) {
	dir, err := r.resolveCodexHomeDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// AccountEnv selects the account in dir through CODEX_HOME, left unset for
// ~/.codex.
func (r *repository) AccountEnv(dir string) core.ProviderEnv {
	defaultDir, _ := userCodexHomeDir()
	return providerkit.AccountEnv(codexHomeEnvVar, dir, defaultDir)
}

// AccountUsage reads the ChatGPT plan's rate limit usage of the account env
// selects, with the login Codex stored in its home. Rig never renews the
// login, which Codex does when it runs.
func (r *repository) AccountUsage(ctx context.Context, env core.ProviderEnv) (*core.AccountUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, providerkit.UsageReadTimeout)
	defer cancel()
	dir, err := r.AccountDir(env)
	if err != nil {
		return nil, err
	}
	return r.usage.Get(dir, func() (*core.AccountUsage, error) {
		auth, err := readAuth(dir)
		if err != nil {
			return nil, err
		}
		headers := map[string]string{"Authorization": "Bearer " + auth.Tokens.AccessToken}
		if accountID := strings.TrimSpace(auth.Tokens.AccountID); accountID != "" {
			headers["ChatGPT-Account-Id"] = accountID
		}
		url := r.usageURL
		if url == "" {
			url = defaultUsageURL
		}
		var response usageResponse
		if err := providerkit.GetUsageJSON(ctx, url, headers, &response); err != nil {
			return nil, err
		}
		return response.accountUsage(time.Now().UTC()), nil
	})
}

// codexAuth is the part of a Codex home's auth.json Rig reads.
type codexAuth struct {
	APIKey *string `json:"OPENAI_API_KEY"`
	Tokens *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func readAuth(dir string) (*codexAuth, error) {
	raw, err := os.ReadFile(filepath.Join(dir, authFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("not logged in: run codex login with this account")
	}
	if err != nil {
		return nil, fmt.Errorf("read codex login: %w", err)
	}
	var auth codexAuth
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil, fmt.Errorf("decode codex login: %w", err)
	}
	if auth.Tokens == nil || strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		if auth.APIKey != nil && strings.TrimSpace(*auth.APIKey) != "" {
			return nil, errors.New("logged in with an API key, which has no plan limits")
		}
		return nil, errors.New("not logged in: run codex login with this account")
	}
	if expiry, ok := tokenExpiry(auth.Tokens.AccessToken); ok && time.Now().After(expiry) {
		return nil, errors.New("login expired: start a Codex session on this account to renew it")
	}
	return &auth, nil
}

// tokenExpiry reads the expiry of a JWT access token, without verifying it.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// usageResponse is the part of the usage endpoint's answer Rig shows.
type usageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit *struct {
		PrimaryWindow   *usageWindow `json:"primary_window"`
		SecondaryWindow *usageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type usageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

func (r usageResponse) accountUsage(now time.Time) *core.AccountUsage {
	usage := &core.AccountUsage{Plan: strings.TrimSpace(r.PlanType), ReportedAt: now}
	if r.RateLimit == nil {
		return usage
	}
	for _, window := range []*usageWindow{r.RateLimit.PrimaryWindow, r.RateLimit.SecondaryWindow} {
		if window == nil {
			continue
		}
		limit := core.UsageLimit{Name: windowName(window.LimitWindowSeconds), UsedPercent: window.UsedPercent}
		// An unused window has not started: its reset is only now plus its
		// length.
		if window.UsedPercent > 0 && window.ResetAt > 0 {
			limit.ResetsAt = time.Unix(window.ResetAt, 0).UTC()
		}
		usage.Limits = append(usage.Limits, limit)
	}
	return usage
}

// windowName names a rate limit window by its length, such as "5h" or "week".
func windowName(seconds int64) string {
	switch {
	case seconds <= 0:
		return "limit"
	case seconds == 7*24*3600:
		return "week"
	case seconds%(24*3600) == 0:
		return strconv.FormatInt(seconds/(24*3600), 10) + "d"
	case seconds%3600 == 0:
		return strconv.FormatInt(seconds/3600, 10) + "h"
	default:
		return strconv.FormatInt(seconds/60, 10) + "m"
	}
}
