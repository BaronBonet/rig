package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
)

const (
	// defaultUsageURL is where Claude Code's /usage reads the plan usage of
	// the logged-in account.
	defaultUsageURL = "https://api.anthropic.com/api/oauth/usage"
	oauthBetaHeader = "oauth-2025-04-20"
	// keychainServicePrefix names the macOS keychain item Claude Code keeps
	// an account's login in.
	keychainServicePrefix = "Claude Code-credentials"
	// credentialsFileName is where Claude Code keeps the login outside the
	// keychain.
	credentialsFileName = ".credentials.json"
)

// AccountDir returns the configuration directory env makes Claude Code use.
func (r *repository) AccountDir(env core.ProviderEnv) (string, error) {
	dir, err := r.resolveConfigDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// AccountEnv selects the account in dir through CLAUDE_CONFIG_DIR, left unset
// for ~/.claude: Claude Code keeps the login of an explicit ~/.claude apart
// from the default one.
func (r *repository) AccountEnv(dir string) core.ProviderEnv {
	defaultDir, _ := userConfigDir()
	return providerkit.AccountEnv(configDirEnvVar, dir, defaultDir)
}

// AccountUsage reads the plan usage of the account env selects the way
// Claude Code's /usage does, with the login Claude Code stored. Rig never
// renews the login: a renewed one would have to be written back where
// Claude Code keeps it, so an expired login is reported instead.
func (r *repository) AccountUsage(ctx context.Context, env core.ProviderEnv) (*core.AccountUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, providerkit.UsageReadTimeout)
	defer cancel()
	setting := configDirSetting(env)
	return r.usage.Get(setting, func() (*core.AccountUsage, error) {
		dir, err := r.AccountDir(env)
		if err != nil {
			return nil, err
		}
		login, err := r.readLogin(ctx, setting, dir)
		if err != nil {
			return nil, err
		}
		url := r.usageURL
		if url == "" {
			url = defaultUsageURL
		}
		var response usageResponse
		if err := providerkit.GetUsageJSON(ctx, url, map[string]string{
			"Authorization":  "Bearer " + login.AccessToken,
			"anthropic-beta": oauthBetaHeader,
			"Content-Type":   "application/json",
		}, &response); err != nil {
			return nil, err
		}
		usage := response.accountUsage()
		usage.Plan = strings.TrimSpace(login.SubscriptionType)
		usage.ReportedAt = time.Now().UTC()
		return usage, nil
	})
}

// configDirSetting is CLAUDE_CONFIG_DIR as Claude Code started with env sees
// it, "" when unset.
func configDirSetting(env core.ProviderEnv) string {
	if value, decided := env.Lookup(configDirEnvVar); decided {
		return value
	}
	return strings.TrimSpace(os.Getenv(configDirEnvVar))
}

// keychainService names the keychain item of the account Claude Code uses
// with CLAUDE_CONFIG_DIR set to setting: a set variable adds the first eight
// hex digits of the SHA-256 of its value, as written, to the default name.
func keychainService(setting string) string {
	if setting == "" {
		return keychainServicePrefix
	}
	sum := sha256.Sum256([]byte(norm.NFC.String(setting)))
	return keychainServicePrefix + "-" + hex.EncodeToString(sum[:])[:8]
}

// storedLogin is the part of Claude Code's stored credentials Rig reads.
type storedLogin struct {
	ClaudeAiOauth *oauthLogin `json:"claudeAiOauth"`
}

type oauthLogin struct {
	AccessToken      string `json:"accessToken"`
	ExpiresAt        int64  `json:"expiresAt"`
	SubscriptionType string `json:"subscriptionType"`
}

// readLogin returns the account's Claude login from the macOS keychain, or
// from the credentials file Claude Code falls back to elsewhere.
func (r *repository) readLogin(ctx context.Context, setting string, dir string) (*oauthLogin, error) {
	var raw string
	if runtime.GOOS == "darwin" {
		result, err := r.runner.Run(ctx, "", "security", "find-generic-password", "-s", keychainService(setting), "-w")
		if err == nil {
			raw = strings.TrimSpace(result.Stdout)
		}
	}
	if raw == "" {
		content, err := os.ReadFile(filepath.Join(dir, credentialsFileName))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read claude login: %w", err)
		}
		raw = strings.TrimSpace(string(content))
	}
	if raw == "" {
		return nil, errors.New("not logged in: run claude with this account and /login")
	}
	// Claude Code may store the item hex-encoded.
	if !strings.HasPrefix(raw, "{") {
		if decoded, err := hex.DecodeString(raw); err == nil {
			raw = string(decoded)
		}
	}

	var stored storedLogin
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("decode claude login: %w", err)
	}
	login := stored.ClaudeAiOauth
	if login == nil || strings.TrimSpace(login.AccessToken) == "" {
		return nil, errors.New("not logged in with a Claude subscription: run claude with this account and /login")
	}
	if login.ExpiresAt > 0 && time.Now().After(time.UnixMilli(login.ExpiresAt)) {
		return nil, errors.New("login expired: start a Claude Code session on this account to renew it")
	}
	return login, nil
}

// usageResponse is the part of the usage endpoint's answer Rig shows. Limits
// is the current form; the named windows are the older one.
type usageResponse struct {
	Limits         []usageLimit `json:"limits"`
	FiveHour       *usageWindow `json:"five_hour"`
	SevenDay       *usageWindow `json:"seven_day"`
	SevenDayOpus   *usageWindow `json:"seven_day_opus"`
	SevenDaySonnet *usageWindow `json:"seven_day_sonnet"`
}

type usageLimit struct {
	Percent *float64 `json:"percent"`
	Scope   *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
	Kind     string `json:"kind"`
	ResetsAt string `json:"resets_at"`
}

type usageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func (r usageResponse) accountUsage() *core.AccountUsage {
	usage := &core.AccountUsage{}
	for _, limit := range r.Limits {
		if limit.Percent == nil {
			continue
		}
		usage.Limits = append(usage.Limits, core.UsageLimit{
			Name:        limit.name(),
			UsedPercent: *limit.Percent,
			ResetsAt:    parseResetTime(limit.ResetsAt),
		})
	}
	if len(usage.Limits) > 0 {
		return usage
	}
	for _, window := range []struct {
		name   string
		window *usageWindow
	}{
		{"5h", r.FiveHour},
		{"week", r.SevenDay},
		{"week opus", r.SevenDayOpus},
		{"week sonnet", r.SevenDaySonnet},
	} {
		if window.window == nil || window.window.Utilization == nil {
			continue
		}
		usage.Limits = append(usage.Limits, core.UsageLimit{
			Name:        window.name,
			UsedPercent: *window.window.Utilization,
			ResetsAt:    parseResetTime(window.window.ResetsAt),
		})
	}
	return usage
}

func (l usageLimit) name() string {
	switch l.Kind {
	case "session":
		return "5h"
	case "weekly_all":
		return "week"
	case "weekly_scoped":
		if l.Scope != nil && l.Scope.Model != nil && strings.TrimSpace(l.Scope.Model.DisplayName) != "" {
			return "week " + strings.ToLower(strings.TrimSpace(l.Scope.Model.DisplayName))
		}
		return "week scoped"
	default:
		return strings.ReplaceAll(l.Kind, "_", " ")
	}
}

func parseResetTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
