package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Task is the durable business record for a task.
//
// It intentionally excludes live runtime observations and derived existence
// checks. Those belong in separate runtime/read-side types rather than on the
// core task record itself.
type Task struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
	// Slug is the stable workspace identifier derived once at task creation from
	// DisplayName and then persisted so branch/worktree/session naming remains
	// stable even if display names collide or later change.
	Slug         string   `json:"slug"`
	Prompt       string   `json:"prompt"`
	DisplayName  string   `json:"display_name"`
	RepoRoot     string   `json:"repo_root"`
	RepoName     string   `json:"repo_name"`
	BranchName   string   `json:"branch_name"`
	WorktreePath string   `json:"worktree_path"`
	TmuxSession  string   `json:"tmux_session"`
	Provider     Provider `json:"provider"`
	// CreationStatus tracks whether the durable task has completed its initial
	// workspace/session setup, or whether it can be retried from CreationStep.
	CreationStatus TaskCreationStatus     `json:"creation_status"`
	CreationStep   TaskCreateProgressStep `json:"creation_step"`
	CreationError  string                 `json:"creation_error"`
	WorkspaceKind  WorkspaceKind          `json:"workspace_kind"`
	// ProviderEnv is the provider configuration the Task's providers run with.
	ProviderEnv ProviderEnv `json:"provider_env,omitempty"`
	// Model and Effort are the launch options the Task's provider starts and
	// resumes with; empty means the provider's own default.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// ParentID names the Task this one is a session of: a child Task shares
	// its parent's workspace and tmux session and runs in TmuxWindow there,
	// so a larger task can have several sessions side by side under one row.
	ParentID string `json:"parent_id,omitempty"`
	// TmuxWindow is the window of TmuxSession the Task's provider runs in;
	// empty means the session's main task window.
	TmuxWindow string `json:"tmux_window,omitempty"`
	// ShelvedAt is when the Task was taken off the current list; zero while
	// it is on it.
	ShelvedAt time.Time `json:"shelved_at,omitzero"`
	// SessionTitle is the title the user last gave one of the Task's provider
	// sessions, which became its DisplayName. A session renames its Task only
	// when that title changes, so a rename made in Rig is not undone.
	SessionTitle string `json:"session_title,omitempty"`
}

// Rename gives the Task a new name, for the new purpose it is being reused
// for, and drops its original ask, which would brief new sessions on the old
// one. It reports whether the name changed.
func (t *Task) Rename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == t.DisplayName {
		return false
	}
	t.DisplayName = name
	t.Prompt = ""
	t.UpdatedAt = time.Now().UTC()
	return true
}

// IsChild reports whether the Task is a session of another Task.
func (t *Task) IsChild() bool {
	return t != nil && strings.TrimSpace(t.ParentID) != ""
}

// IsShelved reports whether the Task is on the shelf rather than the current
// list.
func (t *Task) IsShelved() bool {
	return t != nil && !t.ShelvedAt.IsZero()
}

// LaunchOptions are the provider launch choices kept on a Task: the model and
// effort its provider starts and resumes with. Empty means the provider's own
// default.
type LaunchOptions struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// Launch returns the Task's launch options.
func (t *Task) Launch() LaunchOptions {
	if t == nil {
		return LaunchOptions{}
	}
	return LaunchOptions{Model: strings.TrimSpace(t.Model), Effort: strings.TrimSpace(t.Effort)}
}

// SetLaunch records launch options on the Task.
func (t *Task) SetLaunch(options LaunchOptions) {
	if t == nil {
		return
	}
	t.Model = strings.TrimSpace(options.Model)
	t.Effort = strings.TrimSpace(options.Effort)
}

// ProviderLaunchOptions lists the launch choices one provider accepts, in
// display order, and whether it can write a handoff note from a previous
// session for a new one.
type ProviderLaunchOptions struct {
	Models  []string `json:"models"`
	Efforts []string `json:"efforts"`
	Handoff bool     `json:"handoff"`
}

// LaunchSettings is what the task composer needs to offer launch options:
// each configured provider's choices and the options the user last launched
// each provider with.
type LaunchSettings struct {
	Options  map[Provider]ProviderLaunchOptions `json:"options"`
	Defaults map[Provider]LaunchOptions         `json:"defaults"`
}

// ProviderEnv is the provider configuration environment of the rig window a
// Task came from: every variable the providers read their configuration
// location from (CLAUDE_CONFIG_DIR, CODEX_HOME), with "" for one the window
// left unset, so the provider's default applies. One daemon serves rig
// windows started under different accounts, so a Task carries its own rather
// than inheriting the daemon's. A variable the env does not name, and every
// variable of a nil env, falls back to the daemon's environment.
type ProviderEnv map[string]string

// Lookup returns the value the env gives name and whether the env decides it;
// "" with true means the variable must be unset.
func (e ProviderEnv) Lookup(name string) (string, bool) {
	value, ok := e[name]
	return strings.TrimSpace(value), ok
}

// WorkspaceKind says how a Task's Workspace came to be, which decides what Rig
// may create and remove there.
type WorkspaceKind string

const (
	// WorkspaceKindWorktree is a dedicated worktree and branch Rig created for
	// the Task; cleanup removes the worktree and keeps the branch.
	WorkspaceKindWorktree WorkspaceKind = "worktree"
	// WorkspaceKindFolder is an existing folder, Git or not, that the Task runs
	// in as it is. Several Tasks may share it, and Rig never creates or removes
	// files or branches there beyond provider hook registration.
	WorkspaceKindFolder WorkspaceKind = "folder"
)

// UsesFolderWorkspace reports whether the Task runs in an existing folder
// rather than a worktree Rig created for it.
func (t *Task) UsesFolderWorkspace() bool {
	return t != nil && t.WorkspaceKind == WorkspaceKindFolder
}

// TaskIDEnvVar names the environment variable Rig sets on every Task Session so
// provider hooks can report which Task they belong to, even when several Tasks
// share a workspace.
const TaskIDEnvVar = "RIG_TASK_ID"

type TaskCreationStatus string

const (
	TaskCreationStatusCreating TaskCreationStatus = "creating"
	TaskCreationStatusReady    TaskCreationStatus = "ready"
	TaskCreationStatusFailed   TaskCreationStatus = "failed"
)

type RepoContext struct {
	// Root is the canonical absolute path to the repository root on disk.
	Root       string `json:"root"`
	Name       string `json:"name"`
	BaseBranch string `json:"base_branch"`
}

type TaskSuggestion struct {
	Name       string `json:"name"`
	BranchType string `json:"branch_type"`
}

var validBranchTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"chore":    true,
	"refactor": true,
	"docs":     true,
	"test":     true,
	"style":    true,
	"perf":     true,
	"ci":       true,
	"build":    true,
}

func (s TaskSuggestion) BranchTypeOrDefault() string {
	if s.BranchType != "" && validBranchTypes[s.BranchType] {
		return s.BranchType
	}
	return "feat"
}

type WorkspaceBootstrapSpec struct {
	Files []WorkspaceBootstrapFile
}

type WorkspaceBootstrapFile struct {
	Path     string
	Content  []byte
	FileMode os.FileMode
	// Merge, when non-nil, integrates this file into an existing destination
	// instead of overwriting it: the workspace manager calls it with the
	// current file contents and writes the result, preserving content the
	// provider does not own. When the destination does not exist, Content is
	// written as-is.
	Merge func(existing []byte) ([]byte, error)
}

// TaskStatusPhase is the small application-facing runtime status model used by
// the first hook-driven status stream slice.
type TaskStatusPhase string

const (
	TaskStatusPhaseStarting TaskStatusPhase = "starting"
	TaskStatusPhaseWorking  TaskStatusPhase = "working"
	// TaskStatusPhaseWorkingInBackground means the root agent ended its turn
	// while background work it started is still in flight. The agent is woken
	// when that work completes, so the task is not waiting on the user.
	TaskStatusPhaseWorkingInBackground TaskStatusPhase = "working_in_background"
	TaskStatusPhaseWaitingForInput     TaskStatusPhase = "waiting_for_input"
	TaskStatusPhaseStopped             TaskStatusPhase = "stopped"
)

// TaskBackgroundWork counts the provider background work still in flight when
// the root agent ended its turn. Providers report the full in-flight set at
// every turn end, so the counts are replaced, never accumulated.
type TaskBackgroundWork struct {
	Subagents int `json:"subagents,omitempty"`
	Shells    int `json:"shells,omitempty"`
	Monitors  int `json:"monitors,omitempty"`
	Workflows int `json:"workflows,omitempty"`
	Other     int `json:"other,omitempty"`
}

func (w TaskBackgroundWork) Total() int {
	return w.Subagents + w.Shells + w.Monitors + w.Workflows + w.Other
}

func (w TaskBackgroundWork) IsZero() bool {
	return w.Total() == 0
}

// TaskStatusUpdate is the live status message published by the observer
// process. It is intentionally separate from the durable Task record.
type TaskStatusUpdate struct {
	ObservedAt     time.Time          `json:"observed_at"`
	TaskID         string             `json:"task_id"`
	RawEventName   string             `json:"raw_event_name"`
	Provider       Provider           `json:"provider"`
	Phase          TaskStatusPhase    `json:"phase"`
	BackgroundWork TaskBackgroundWork `json:"background_work"`
}

// TaskSessionRuntimeState is the current tmux-side state of a task session.
type TaskSessionRuntimeState struct {
	ActiveCommands                  []string
	Exists                          bool
	ChildProcessEvidenceUnavailable bool
}

type TaskActivityRole string

const (
	TaskActivityRoleUser      TaskActivityRole = "user"
	TaskActivityRoleAssistant TaskActivityRole = "assistant"
)

// TaskActivityEvent is the compact persisted read model used by the detail
// panel to show the last human prompt and recent LLM actions for a task.
type TaskActivityEvent struct {
	ObservedAt time.Time        `json:"observed_at"`
	TaskID     string           `json:"task_id"`
	TurnID     string           `json:"turn_id"`
	EventName  string           `json:"event_name"`
	Role       TaskActivityRole `json:"role"`
	Text       string           `json:"text"`
}

// TaskResumeMetadata is the minimal provider runtime state needed to reconnect
// a task session after its tmux session has been lost.
type TaskResumeMetadata struct {
	ObservedAt time.Time `json:"observed_at"`
	TaskID     string    `json:"task_id"`
	SessionID  string    `json:"session_id"`
	Provider   Provider  `json:"provider"`
}

type TaskProviderSession struct {
	FirstObservedAt   time.Time `json:"first_observed_at"`
	LastObservedAt    time.Time `json:"last_observed_at"`
	TaskID            string    `json:"task_id"`
	Provider          Provider  `json:"provider"`
	ProviderSessionID string    `json:"provider_session_id"`
	TranscriptPath    string    `json:"transcript_path"`
	StartSource       string    `json:"start_source"`
	LastEventName     string    `json:"last_event_name"`
	Model             string    `json:"model"`
	Cwd               string    `json:"cwd"`
}

type SessionTokenUsage struct {
	// ContextTokens is the size of the session's latest request: how full its
	// context window is now. The other counts add up every request.
	ContextTokens            int `json:"context_tokens"`
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CachedInputTokens        int `json:"cached_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	ReasoningOutputTokens    int `json:"reasoning_output_tokens"`
	TotalTokens              int `json:"total_tokens"`
}

func (u SessionTokenUsage) IsZero() bool {
	return u.ContextTokens == 0 &&
		u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

type TaskTokenUsage struct {
	SessionCount int `json:"session_count"`
	// Latest is the usage of the task's most recently active session, the one
	// a new session would take over from. The counts below add up all its
	// sessions.
	Latest                   SessionTokenUsage `json:"latest"`
	InputTokens              int               `json:"input_tokens"`
	OutputTokens             int               `json:"output_tokens"`
	CachedInputTokens        int               `json:"cached_input_tokens"`
	CacheCreationInputTokens int               `json:"cache_creation_input_tokens"`
	ReasoningOutputTokens    int               `json:"reasoning_output_tokens"`
	TotalTokens              int               `json:"total_tokens"`
}

func (u TaskTokenUsage) IsZero() bool {
	return u.SessionCount == 0 &&
		u.Latest.IsZero() &&
		u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

// RepoSettings is a repository's own configuration, from its .rig.yaml, for
// the worktree Tasks Rig creates in it.
type RepoSettings struct {
	// BaseBranch is the branch new worktree Tasks start from, fetched from
	// origin first. Empty means the branch the main checkout has checked out.
	BaseBranch string
	// WorktreeName names a new Task's worktree folder from {repo} and {slug}.
	// Empty means "{repo}_{slug}".
	WorktreeName string
}

// ProviderSessionSummary is a provider session found in the provider's own
// session store rather than through Rig, offered for import as a Task.
type ProviderSessionSummary struct {
	LastActiveAt   time.Time `json:"last_active_at"`
	Provider       Provider  `json:"provider"`
	SessionID      string    `json:"session_id"`
	Title          string    `json:"title"`
	Cwd            string    `json:"cwd"`
	TranscriptPath string    `json:"transcript_path"`
}

// FolderContains reports whether path is folder itself or lies below it. Both
// are clean absolute paths.
func FolderContains(folder string, path string) bool {
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// SessionFileChange is one file edit a Provider session made, recovered from
// its Provider transcript (including the transcripts of its subagents).
type SessionFileChange struct {
	ObservedAt time.Time
	Path       string
}

// WorktreeRef identifies the Git worktree a directory belongs to and the branch
// it has checked out now. Branch is empty for a detached HEAD.
type WorktreeRef struct {
	Root     string
	RepoName string
	Branch   string
}

// TaskWorktreeRecord is the durable observation behind a Task's touched
// worktrees: the branch the worktree had when the Task's latest edit there was
// first observed. Comparing it with the branch checked out now tells whether the
// worktree has since moved on to other work.
type TaskWorktreeRecord struct {
	LastEditAt   time.Time
	TaskID       string
	WorktreePath string
	RepoName     string
	Branch       string
	EditCount    int
}

// TaskWorktree is a worktree the Task has edited that still exists, as it
// stands now. Branch is what the worktree has checked out now; EditedBranch is
// what it had at the Task's latest edit there.
type TaskWorktree struct {
	LastEditAt   time.Time `json:"last_edit_at"`
	WorktreePath string    `json:"worktree_path"`
	RepoName     string    `json:"repo_name"`
	Branch       string    `json:"branch"`
	EditedBranch string    `json:"edited_branch"`
	EditCount    int       `json:"edit_count"`
}

// BranchChanged reports whether the worktree has checked out another branch
// since the Task last edited it, which usually means other work reused it.
func (w TaskWorktree) BranchChanged() bool {
	return w.Branch != w.EditedBranch
}

// Provider identifies the supported interactive runtime backing a task.
type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

// Canonical provider hook event names. HookEventInput.EventName carries these
// values across the provider seam: provider adapters declare which of them
// they observe (and forward them by name), and task observation consumes them
// for status, activity, and provider adoption.
const (
	HookEventSessionStart      = "SessionStart"
	HookEventUserPromptSubmit  = "UserPromptSubmit"
	HookEventPreToolUse        = "PreToolUse"
	HookEventPostToolUse       = "PostToolUse"
	HookEventStop              = "Stop"
	HookEventNotification      = "Notification"
	HookEventPermissionRequest = "PermissionRequest"
)

// SupportedProviders returns every provider Rig knows how to integrate with,
// in stable display order. Supported is not the same as configured: a provider
// becomes usable only after the user enables it during provider setup.
func SupportedProviders() []Provider {
	return []Provider{ProviderCodex, ProviderClaude}
}

func IsSupportedProvider(provider Provider) bool {
	for _, supported := range SupportedProviders() {
		if provider == supported {
			return true
		}
	}
	return false
}

// ProviderSetup is the user-level provider configuration produced by provider
// setup. It lives in user config, never in the task database.
type ProviderSetup struct {
	Configured []Provider `json:"configured"`
	Default    Provider   `json:"default"`
}

func (s ProviderSetup) IsConfigured(provider Provider) bool {
	for _, configured := range s.Configured {
		if configured == provider {
			return true
		}
	}
	return false
}

func (s ProviderSetup) Validate() error {
	if len(s.Configured) == 0 {
		return fmt.Errorf("provider setup requires at least one configured provider")
	}
	seen := make(map[Provider]bool, len(s.Configured))
	for _, provider := range s.Configured {
		if !IsSupportedProvider(provider) {
			return fmt.Errorf("provider %q is not a supported provider", provider)
		}
		if seen[provider] {
			return fmt.Errorf("provider %q is configured more than once", provider)
		}
		seen[provider] = true
	}
	if s.Default == "" {
		return fmt.Errorf("provider setup requires a default provider")
	}
	if !s.IsConfigured(s.Default) {
		return fmt.Errorf("default provider %q is not a configured provider", s.Default)
	}
	return nil
}

// ProviderDetection reports whether one supported provider passed Rig's
// provider setup checks on this machine.
type ProviderDetection struct {
	Provider Provider `json:"provider"`
	Ready    bool     `json:"ready"`
	Detail   string   `json:"detail,omitempty"`
}

// TaskSessionLaunchSpec is the handoff from a provider client to the tmux
// session client for starting an interactive task session.
//
// This is not a domain object. It is an application-facing integration DTO that
// describes how the tmux adapter should start the provider's CLI.
type TaskSessionLaunchSpec struct {
	// Command is the argv launched in the task's task tmux window, for example
	// []string{"codex"}.
	Command []string
	// ReadyMarker is the terminal prompt marker emitted by the provider when it is
	// ready to receive interactive input. The tmux session client waits for this
	// marker before typing PrefillInput into the window.
	ReadyMarker string
	// PrefillInput is the text typed into the interactive provider after the
	// command has started and the ReadyMarker has appeared. For create-task,
	// this is the drafted task prompt that is placed into the fresh task
	// session without being submitted.
	PrefillInput []string
}
