package tui

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/BaronBonet/rig/internal/core"
)

// accountUsageMaxAge is how long a usage reading is shown before the next
// look at it reads it again.
const accountUsageMaxAge = time.Minute

// providerAccountsLoadedMsg carries the accounts this window's tasks may run
// under.
type providerAccountsLoadedMsg struct {
	err      error
	accounts []core.ProviderAccount
}

// accountUsageMsg carries one reading of an account's usage.
type accountUsageMsg struct {
	err   error
	usage *core.AccountUsage
	key   string
}

// accountUsageState is the latest reading of one account's usage.
type accountUsageState struct {
	err     error
	usage   *core.AccountUsage
	readAt  time.Time
	loading bool
}

func accountKey(account core.ProviderAccount) string {
	return string(account.Provider) + "\x00" + account.Dir
}

func listProviderAccountsCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	folder string,
	env core.ProviderEnv,
) tea.Cmd {
	return func() tea.Msg {
		accounts, err := frontend.ListProviderAccounts(ctx, folder, env)
		return providerAccountsLoadedMsg{accounts: accounts, err: err}
	}
}

func accountUsageCmd(ctx context.Context, frontend core.TaskFrontend, account core.ProviderAccount) tea.Cmd {
	return func() tea.Msg {
		usage, err := frontend.GetAccountUsage(ctx, account)
		return accountUsageMsg{key: accountKey(account), usage: usage, err: err}
	}
}

// accountsFor lists the accounts offered for provider, the window's own first.
func (m model) accountsFor(provider core.Provider) []core.ProviderAccount {
	var accounts []core.ProviderAccount
	for _, account := range m.accounts {
		if account.Provider == provider {
			accounts = append(accounts, account)
		}
	}
	return accounts
}

// draftAccount is the account the draft runs under: the one chosen, or else
// the task's own for a new session and the window's for a new task. It is
// nil for a new session whose task runs under an account not offered here,
// which the session keeps.
func (m model) draftAccount() *core.ProviderAccount {
	accounts := m.accountsFor(m.effectiveCreateProvider())
	for index := range accounts {
		if m.draft.account != "" && accounts[index].Dir == m.draft.account {
			return &accounts[index]
		}
	}
	for index := range accounts {
		account := &accounts[index]
		if m.draft.forTask == nil && account.Window {
			return account
		}
		if m.draft.forTask != nil && m.draft.forTask.ProviderEnv.Selects(account.Env) {
			return account
		}
	}
	return nil
}

// taskAccount is the account, of those offered here, the task's provider runs
// under; nil when it is none of them.
func (m model) taskAccount(task *core.Task) *core.ProviderAccount {
	if task == nil {
		return nil
	}
	accounts := m.accountsFor(task.Provider)
	for index := range accounts {
		if task.ProviderEnv.Selects(accounts[index].Env) {
			return &accounts[index]
		}
	}
	return nil
}

// cycleCreateAccount advances the draft to the provider's next account.
func (m *model) cycleCreateAccount() tea.Cmd {
	accounts := m.accountsFor(m.effectiveCreateProvider())
	if len(accounts) < 2 {
		return nil
	}
	next := accounts[0]
	if current := m.draftAccount(); current != nil {
		for index, account := range accounts {
			if account.Dir == current.Dir {
				next = accounts[(index+1)%len(accounts)]
				break
			}
		}
	}
	m.draft.account = next.Dir
	// Another account may not offer the model or effort chosen so far.
	models, efforts := m.launchChoices(m.effectiveCreateProvider())
	if !slices.Contains(models, m.draft.model) {
		m.draft.model = ""
	}
	if !slices.Contains(efforts, m.draft.effort) {
		m.draft.effort = ""
	}
	return m.readAccountUsage(accounts, false)
}

// draftTaskEnv is the provider configuration a new task from the draft runs
// with: the window's, under the draft's account.
func (m model) draftTaskEnv() core.ProviderEnv {
	if account := m.draftAccount(); account != nil {
		return m.providerEnv.With(account.Env)
	}
	return m.providerEnv
}

// draftSessionEnv is the provider configuration a new session from the draft
// runs with, or nil when it keeps its task's.
func (m model) draftSessionEnv() core.ProviderEnv {
	task := m.draft.forTask
	account := m.draftAccount()
	if task == nil || account == nil || task.ProviderEnv.Selects(account.Env) {
		return nil
	}
	return task.ProviderEnv.With(account.Env)
}

// configuredAccounts lists the accounts of the configured providers.
func (m model) configuredAccounts() []core.ProviderAccount {
	var accounts []core.ProviderAccount
	for _, provider := range m.configuredProviders() {
		accounts = append(accounts, m.accountsFor(provider)...)
	}
	return accounts
}

// readAccountUsage reads the usage of the accounts whose reading is missing
// or old, or of every one with force, unless a reading is under way.
func (m *model) readAccountUsage(accounts []core.ProviderAccount, force bool) tea.Cmd {
	if m.accountUsage == nil {
		m.accountUsage = make(map[string]accountUsageState)
	}
	var cmds []tea.Cmd
	now := time.Now()
	for _, account := range accounts {
		key := accountKey(account)
		state := m.accountUsage[key]
		if state.loading || (!force && !state.readAt.IsZero() && now.Sub(state.readAt) < accountUsageMaxAge) {
			continue
		}
		state.loading = true
		m.accountUsage[key] = state
		cmds = append(cmds, accountUsageCmd(m.statusContext, m.frontend, account))
	}
	return tea.Batch(cmds...)
}

func (m model) handleAccountUsage(msg accountUsageMsg) (tea.Model, tea.Cmd) {
	if m.accountUsage == nil {
		m.accountUsage = make(map[string]accountUsageState)
	}
	m.accountUsage[msg.key] = accountUsageState{usage: msg.usage, err: msg.err, readAt: time.Now()}
	return m, nil
}

func (m model) handleProviderAccountsLoaded(msg providerAccountsLoadedMsg) (tea.Model, tea.Cmd) {
	m.accountsErr = msg.err
	if msg.err != nil {
		return m, nil
	}
	m.accounts = msg.accounts
	switch m.mode {
	case modeAccounts:
		return m, m.readAccountUsage(m.configuredAccounts(), false)
	case modePromptInput:
		return m, m.readAccountUsage(m.accountsFor(m.effectiveCreateProvider()), false)
	default:
		return m, nil
	}
}

// enterAccountsMode opens the accounts screen: the accounts tasks started in
// this window can run under, and how much of each one's plan is used.
func (m model) enterAccountsMode() (tea.Model, tea.Cmd) {
	if m.pending != opNone || !m.transition(modeAccounts) {
		return m, nil
	}
	return m, tea.Batch(
		listProviderAccountsCmd(m.statusContext, m.frontend, m.launchCwd, m.providerEnv),
		m.readAccountUsage(m.configuredAccounts(), false),
	)
}

func (m model) updateAccounts(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "u":
		return m.handleBack()
	case "r":
		// The accounts are read again too: the user config may have changed.
		return m, tea.Batch(
			listProviderAccountsCmd(m.statusContext, m.frontend, m.launchCwd, m.providerEnv),
			m.readAccountUsage(m.configuredAccounts(), true),
		)
	default:
		return m, nil
	}
}

func (m model) accountsView() string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("accounts")))
	builder.WriteString(errorBlock(m.accountsErr))
	builder.WriteString(dimStyle.Render(
		"The accounts tasks started here can run under, and how much of each plan's limits is used.",
	) + "\n\n")

	accounts := m.configuredAccounts()
	if m.accounts == nil && m.accountsErr == nil {
		builder.WriteString(dimStyle.Render("Reading accounts...") + "\n")
	}
	nameWidth := 0
	for _, account := range accounts {
		nameWidth = max(nameWidth, len(account.Name))
	}
	var provider core.Provider
	for _, account := range accounts {
		if account.Provider != provider {
			if provider != "" {
				builder.WriteString("\n")
			}
			provider = account.Provider
			builder.WriteString(providerStyle(string(provider)).Render(string(provider)) + "\n")
		}
		line := "  " + primaryStyle.Render(padRightVisible(account.Name, nameWidth)) +
			"  " + dimStyle.Render(homeRelativeDir(account.Dir))
		if account.Window {
			line += mutedStyle.Render("  default here")
		}
		builder.WriteString(line + "\n")
		builder.WriteString("    " + m.accountUsageText(account, false) + "\n")
	}

	builder.WriteString("\n")
	builder.WriteString(footerKeybinds([2]string{"r", "refresh"}, [2]string{"esc", "back"}))
	return builder.String()
}

// accountUsageText shows the account's latest usage reading, each limit with
// its reset time, or only the limits when compact.
func (m model) accountUsageText(account core.ProviderAccount, compact bool) string {
	state, ok := m.accountUsage[accountKey(account)]
	switch {
	case !ok:
		return ""
	case state.err != nil:
		if compact {
			return dimStyle.Render("usage unavailable")
		}
		return warningStyle.Render("usage unavailable: " + state.err.Error())
	case state.usage == nil && state.loading:
		return dimStyle.Render("reading usage...")
	case state.usage == nil:
		return ""
	}

	usage := state.usage
	var parts []string
	if usage.Plan != "" && !compact {
		parts = append(parts, mutedStyle.Render(usage.Plan))
	}
	now := time.Now()
	for _, limit := range usage.Limits {
		used := limit.UsedPercent
		resets := limit.ResetsAt
		// A window that has started over since the reading is unused.
		if !resets.IsZero() && resets.Before(now) {
			used, resets = 0, time.Time{}
		}
		text := limit.Name + " " + usagePercentStyle(used).Render(fmt.Sprintf("%.0f%%", math.Round(used)))
		if !compact && !resets.IsZero() {
			text += mutedStyle.Render(" resets " + resetTimeText(resets, now))
		}
		parts = append(parts, text)
	}
	if len(usage.Limits) == 0 {
		parts = append(parts, dimStyle.Render("no limits reported"))
	}
	if !compact && !usage.ReportedAt.IsZero() && now.Sub(usage.ReportedAt) > 5*time.Minute {
		parts = append(parts, mutedStyle.Render("as of "+usage.ReportedAt.In(now.Location()).Format("15:04")))
	}
	return strings.Join(parts, mutedStyle.Render(" · "))
}

// usagePercentStyle colours a used share of a limit by how close it is.
func usagePercentStyle(used float64) lipgloss.Style {
	switch {
	case used >= 90:
		return errorStyle
	case used >= 70:
		return warningStyle
	default:
		return primaryStyle
	}
}

// resetTimeText shows when a limit resets, in now's time zone: the time
// today, the weekday and time within a week, the date after that.
func resetTimeText(resets time.Time, now time.Time) string {
	local := resets.In(now.Location())
	switch {
	case local.YearDay() == now.YearDay() && local.Year() == now.Year():
		return local.Format("15:04")
	case local.Sub(now) < 6*24*time.Hour:
		return local.Format("Mon 15:04")
	default:
		return local.Format("Jan 2 15:04")
	}
}

// draftAccountLine shows the account the draft runs under, with its usage.
func (m model) draftAccountLine() string {
	accounts := m.accountsFor(m.effectiveCreateProvider())
	if len(accounts) == 0 {
		return ""
	}
	label := mutedStyle.Render("account   ")
	account := m.draftAccount()
	if account == nil {
		return label + primaryStyle.Render("the task's") + mutedStyle.Render("  ·  ") +
			keybindStyle.Render("shift+tab") + mutedStyle.Render(" cycle")
	}
	line := label + primaryStyle.Render(account.Name)
	if usage := m.accountUsageText(*account, true); usage != "" {
		line += mutedStyle.Render("  ") + usage
	}
	if len(accounts) > 1 {
		line += mutedStyle.Render("  ·  ") + keybindStyle.Render("shift+tab") + mutedStyle.Render(" cycle")
	}
	return line
}

// homeRelativeDir shows a directory under the home directory as ~/...
func homeRelativeDir(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(dir, home+"/") {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}
