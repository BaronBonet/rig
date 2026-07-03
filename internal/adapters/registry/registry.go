// Package registry composes the provider adapter clients and hook routes for
// every supported provider. It is the single place where composition code
// learns which providers Rig supports; whether a provider is configured is a
// user-config concern handled by the provider config store and task service.
package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/adapters/client/claude"
	"github.com/BaronBonet/rig/internal/adapters/client/codex"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

type Dependencies struct {
	Runner         subprocess.Runner
	Codex          codex.Config
	Claude         claude.Config
	HookListenAddr string
	HookSecret     string
}

// NewProviderClients builds the adapter client for every supported provider.
// Hook routes and clients exist for all supported providers so the daemon
// never needs a restart when provider setup changes; service-level checks
// decide whether a provider is actually usable.
func NewProviderClients(deps Dependencies) map[core.Provider]core.ProviderClient {
	return map[core.Provider]core.ProviderClient{
		core.ProviderCodex: codex.New(
			deps.Runner,
			deps.Codex,
			codex.NewHookForwardingConfig(deps.HookListenAddr, deps.HookSecret),
		),
		core.ProviderClaude: claude.New(
			deps.Runner,
			deps.Claude,
			claude.NewHookForwardingConfig(deps.HookListenAddr, deps.HookSecret),
		),
	}
}

// LoadOrCreateHookSecret returns the persistent secret provider hook
// forwarding authenticates with. The secret must survive daemon restarts:
// forwarder scripts embed it when they are written, and a per-run secret
// would silently orphan every hook source that outlives the daemon, such as
// running provider sessions and manually launched providers.
func LoadOrCreateHookSecret(path string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if secret := strings.TrimSpace(string(raw)); secret != "" {
			return secret, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read hook secret %s: %w", path, err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate hook secret: %w", err)
	}
	secret := hex.EncodeToString(buf)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create hook secret directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write hook secret %s: %w", path, err)
	}

	return secret, nil
}

// RefreshProviderEnvironments rewrites the hook forwarding material for every
// configured provider so scripts written by an earlier daemon stay valid
// after a restart. Failures degrade observability but must not stop the
// daemon, so they are reported without aborting.
func RefreshProviderEnvironments(
	ctx context.Context,
	providers map[core.Provider]core.ProviderClient,
	store core.ProviderConfigStore,
) []error {
	if store == nil {
		return nil
	}
	setup, err := store.GetProviderSetup(ctx)
	if err != nil || setup == nil {
		return nil
	}

	var errs []error
	for _, provider := range setup.Configured {
		providerClient, ok := providers[provider]
		if !ok {
			continue
		}
		if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
			errs = append(errs, fmt.Errorf("refresh %s session environment: %w", provider, err))
		}
	}
	return errs
}

// NewHookRoutes returns the daemon hook routes for every supported provider.
func NewHookRoutes(
	service core.TaskService,
	now func() time.Time,
	hookSecret string,
) []core.TaskDaemonHookRoute {
	var routes []core.TaskDaemonHookRoute
	routes = append(routes, codex.NewHookRoutes(service, now, hookSecret)...)
	routes = append(routes, claude.NewHookRoutes(service, now, hookSecret)...)
	return routes
}
