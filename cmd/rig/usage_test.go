package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// Resets show in the zone of the time usage is read at, wherever they are
// reported from.
var (
	usageNow      = time.Date(2026, time.October, 13, 12, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	fiveHourReset = time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	weekReset     = time.Date(2026, time.October, 18, 10, 0, 0, 0, time.UTC)
)

const (
	fiveHourResetText = " resets Tue Oct 13 14:00"
	weekResetText     = " resets Sun Oct 18 12:00"
)

func TestUsageText_PrintsThePlanThenEachLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reading accountUsageReading
		want    string
	}{
		{
			name: "plan first, then limits with their reset",
			reading: accountUsageReading{usage: &core.AccountUsage{
				Plan: "max",
				Limits: []core.UsageLimit{
					{Name: "5h", UsedPercent: 42, ResetsAt: fiveHourReset},
					{Name: "week", UsedPercent: 23, ResetsAt: weekReset},
					{Name: "week fable", UsedPercent: 15},
				},
			}},
			want: "max · 5h 42%" + fiveHourResetText +
				" · week 23%" + weekResetText + " · week fable 15%",
		},
		{
			name: "no plan",
			reading: accountUsageReading{usage: &core.AccountUsage{
				Limits: []core.UsageLimit{{Name: "5h", UsedPercent: 10}},
			}},
			want: "5h 10%",
		},
		{
			name: "percentages are rounded",
			reading: accountUsageReading{usage: &core.AccountUsage{
				Limits: []core.UsageLimit{{Name: "5h", UsedPercent: 12.5}, {Name: "week", UsedPercent: 41.4}},
			}},
			want: "5h 13% · week 41%",
		},
		{
			name: "a window that reset before now starts over at zero without a reset",
			reading: accountUsageReading{usage: &core.AccountUsage{
				Plan: "plus",
				Limits: []core.UsageLimit{
					{Name: "5h", UsedPercent: 80, ResetsAt: usageNow.Add(-time.Minute)},
					{Name: "week", UsedPercent: 30, ResetsAt: weekReset},
				},
			}},
			want: "plus · 5h 0% · week 30%" + weekResetText,
		},
		{
			name:    "a plan without limits",
			reading: accountUsageReading{usage: &core.AccountUsage{Plan: "plus"}},
			want:    "plus · no limits reported",
		},
		{
			name:    "neither a plan nor limits",
			reading: accountUsageReading{usage: &core.AccountUsage{}},
			want:    "no limits reported",
		},
		{
			name:    "an error",
			reading: accountUsageReading{err: errors.New("login expired: start a session to renew it")},
			want:    "usage unavailable: login expired: start a session to renew it",
		},
		{
			name: "an error wins over a reading",
			reading: accountUsageReading{
				usage: &core.AccountUsage{Plan: "max"},
				err:   errors.New("boom"),
			},
			want: "usage unavailable: boom",
		},
		{
			name: "no reading at all",
			want: "usage unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, usageText(test.reading, usageNow))
		})
	}
}

func TestWriteAccountUsage_GroupsAccountsUnderTheirProvider(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	accounts := []core.ProviderAccount{
		{Provider: core.ProviderClaude, Name: "main", Dir: "/home/me/.claude", Window: true},
		{Provider: core.ProviderClaude, Name: "work", Dir: "/home/me/.claude-work"},
		{Provider: core.ProviderCodex, Name: "personal", Dir: "/home/me-other/.codex"},
	}
	readings := []accountUsageReading{
		{usage: &core.AccountUsage{
			Plan: "max",
			Limits: []core.UsageLimit{
				{Name: "5h", UsedPercent: 42, ResetsAt: fiveHourReset},
				{Name: "week", UsedPercent: 24},
			},
		}},
		{err: errors.New("login expired")},
		{usage: &core.AccountUsage{
			Plan:   "plus",
			Limits: []core.UsageLimit{{Name: "5h", UsedPercent: 80, ResetsAt: usageNow.Add(-time.Hour)}},
		}},
	}

	var out bytes.Buffer
	writeAccountUsage(&out, accounts, readings, usageNow)

	require.Equal(t, strings.Join([]string{
		"claude",
		"  main      ~/.claude  (default here)",
		"    max · 5h 42%" + fiveHourResetText + " · week 24%",
		"  work      ~/.claude-work",
		"    usage unavailable: login expired",
		"",
		"codex",
		"  personal  /home/me-other/.codex",
		"    plus · 5h 0%",
		"",
	}, "\n"), out.String(),
		"names line up across providers, a dir under the home dir shows ~, "+
			"and only the window's own account is marked as the default")
}

func TestWriteAccountUsage_ShowsDirsAsTheyAreWithoutAHomeDir(t *testing.T) {
	t.Setenv("HOME", "")
	accounts := []core.ProviderAccount{{Provider: core.ProviderClaude, Name: "main", Dir: "/home/me/.claude"}}

	var out bytes.Buffer
	writeAccountUsage(&out, accounts, []accountUsageReading{{err: errors.New("boom")}}, usageNow)

	require.Equal(t, "claude\n  main  /home/me/.claude\n    usage unavailable: boom\n", out.String())
}

func TestWriteAccountUsage_WithoutAccountsPointsToSetup(t *testing.T) {
	var out bytes.Buffer

	writeAccountUsage(&out, nil, nil, usageNow)

	require.Equal(t, "No accounts: run rig setup to configure a provider.\n", out.String())
}
