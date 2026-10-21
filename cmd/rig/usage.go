package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BaronBonet/rig/internal/composition/providerregistry"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/infrastructure"
)

// usageWait bounds how long rig usage waits for the daemon's readings.
const usageWait = 30 * time.Second

// accountUsageReading is one account's usage, or why it could not be read.
type accountUsageReading struct {
	usage *core.AccountUsage
	err   error
}

// runUsage prints the accounts tasks started in the current folder can run
// under, as a rig window here would offer them, with each plan's usage.
func runUsage(stdout io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return err
	}
	folder, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	adapter, err := newClientTaskDaemon(cfg)
	if err != nil {
		return err
	}
	if err := adapter.EnsureRunning(ctx); err != nil {
		return err
	}
	frontend := adapter.Frontend()
	if frontend == nil {
		return fmt.Errorf("task frontend not configured")
	}

	// The daemon gives up on a reading by itself; this bounds the wait for it.
	ctx, cancelRead := context.WithTimeout(ctx, usageWait)
	defer cancelRead()
	accounts, err := frontend.ListProviderAccounts(ctx, folder, providerregistry.CaptureProviderEnv(os.LookupEnv))
	if err != nil {
		return err
	}
	readings := make([]accountUsageReading, len(accounts))
	var wg sync.WaitGroup
	for index, account := range accounts {
		wg.Go(func() {
			usage, err := frontend.GetAccountUsage(ctx, account)
			readings[index] = accountUsageReading{usage: usage, err: err}
		})
	}
	wg.Wait()

	writeAccountUsage(stdout, accounts, readings, time.Now())
	return nil
}

// writeAccountUsage prints the accounts by provider, each with its usage.
func writeAccountUsage(w io.Writer, accounts []core.ProviderAccount, readings []accountUsageReading, now time.Time) {
	if len(accounts) == 0 {
		_, _ = fmt.Fprintln(w, "No accounts: run rig setup to configure a provider.")
		return
	}
	nameWidth := 0
	for _, account := range accounts {
		nameWidth = max(nameWidth, len(account.Name))
	}
	home, _ := os.UserHomeDir()
	var provider core.Provider
	for index, account := range accounts {
		if account.Provider != provider {
			if provider != "" {
				_, _ = fmt.Fprintln(w)
			}
			provider = account.Provider
			_, _ = fmt.Fprintln(w, provider)
		}
		dir := account.Dir
		if home != "" && strings.HasPrefix(dir, home+"/") {
			dir = "~" + strings.TrimPrefix(dir, home)
		}
		line := fmt.Sprintf("  %-*s  %s", nameWidth, account.Name, dir)
		if account.Window {
			line += "  (default here)"
		}
		_, _ = fmt.Fprintln(w, line)
		_, _ = fmt.Fprintln(w, "    "+usageText(readings[index], now))
	}
}

func usageText(reading accountUsageReading, now time.Time) string {
	if reading.err != nil {
		return "usage unavailable: " + reading.err.Error()
	}
	if reading.usage == nil {
		return "usage unavailable"
	}
	var parts []string
	if reading.usage.Plan != "" {
		parts = append(parts, reading.usage.Plan)
	}
	for _, limit := range reading.usage.Limits {
		used, resets := limit.UsedPercent, limit.ResetsAt
		if !resets.IsZero() && resets.Before(now) {
			used, resets = 0, time.Time{}
		}
		text := fmt.Sprintf("%s %.0f%%", limit.Name, math.Round(used))
		if !resets.IsZero() {
			text += " resets " + resets.In(now.Location()).Format("Mon Jan 2 15:04")
		}
		parts = append(parts, text)
	}
	if len(reading.usage.Limits) == 0 {
		parts = append(parts, "no limits reported")
	}
	return strings.Join(parts, " · ")
}
