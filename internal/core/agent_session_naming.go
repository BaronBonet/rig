package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// agentNamingTimeout bounds naming one agent, a ticket lookup included.
const agentNamingTimeout = 2 * time.Minute

// maxAgentNamingEntries bounds the record of conversations Rig has started
// naming. An entry is one conversation of one agent session, so the bound
// only matters for a long-running daemon.
const maxAgentNamingEntries = 1024

// SessionStart sources of a Provider session that goes on with an earlier
// conversation rather than one the agent starts afresh.
const (
	sessionStartSourceResume  = "resume"
	sessionStartSourceCompact = "compact"
	sessionStartSourceFork    = "fork"
)

// agentNamingKey identifies one conversation of one agent session. Each is
// named once while its record lasts, so a naming that fails is not retried on
// every status cycle.
type agentNamingKey struct {
	agentSessionID    string
	providerSessionID string
}

// unnamed reports whether the agent session's current conversation has no
// name: the agent was never named, or it has started a conversation afresh
// since.
func (s AgentSession) unnamed() bool {
	return s.Name == "" || s.NamedProviderSessionID != s.ProviderSessionID
}

// startsConversationAfresh reports whether an event moves an agent to a
// conversation it starts afresh, as /clear and Codex's /new do, so its first
// prompt renames the agent. A conversation that goes on with an earlier one,
// as a resume, compaction or fork does, keeps the agent's name. Only a
// SessionStart says which: an agent that moves on a prompt, as into a fork
// Codex reports no SessionStart for, goes on with its work.
func startsConversationAfresh(input HookEventInput) bool {
	if strings.TrimSpace(input.EventName) != HookEventSessionStart {
		return false
	}
	switch strings.TrimSpace(input.StartSource) {
	case sessionStartSourceResume, sessionStartSourceCompact, sessionStartSourceFork:
		return false
	default:
		return true
	}
}

// nameAgentSessions starts naming each live agent session of a task whose
// current conversation has a first prompt but no name, while the task has two
// or more: only then does each show as a sub-row under the task. Naming runs
// in the background, and the task's status is published again once an agent
// is named.
func (o *taskObservation) nameAgentSessions(
	ctx context.Context,
	task *Task,
	evidence agentSessionEvidence,
	liveView agentSessionLiveView,
) {
	live := slices.DeleteFunc(slices.Clone(evidence.open), liveView.isLost)
	if task == nil || len(live) < 2 {
		return
	}
	for _, session := range live {
		// An agent session without any prompt has nothing to be named from.
		if !session.unnamed() || strings.TrimSpace(evidence.prompts[session.ID]) == "" {
			continue
		}
		key := agentNamingKey{agentSessionID: session.ID, providerSessionID: session.ProviderSessionID}
		if _, started := o.agentNamings.get(key); started {
			continue
		}
		prompt, err := o.conversationFirstPrompt(ctx, session)
		if err != nil || prompt == "" {
			continue
		}
		if !o.agentNamings.putIfAbsent(key, struct{}{}) {
			continue
		}
		o.startBackground(func() {
			// Naming is best effort: an agent it fails for keeps showing its
			// latest prompt.
			_ = o.nameAgentSession(ctx, task, session, prompt)
		})
	}
}

// conversationFirstPrompt returns the first prompt an agent session received
// in its current conversation, which began when Rig first observed its
// Provider session, or "" before there is one. All of the prompts of an agent
// session whose conversation Rig never observed count.
func (o *taskObservation) conversationFirstPrompt(ctx context.Context, session AgentSession) (string, error) {
	history, err := o.tasks.ListTaskProviderSessions(ctx, session.TaskID)
	if err != nil {
		return "", fmt.Errorf("list task provider sessions: %w", err)
	}
	var startedAt time.Time
	for _, entry := range conversationHistory(history, session) {
		if startedAt.IsZero() || entry.FirstObservedAt.Before(startedAt) {
			startedAt = entry.FirstObservedAt
		}
	}

	prompts, err := o.tasks.ListAgentSessionPrompts(ctx, session.ID)
	if err != nil {
		return "", fmt.Errorf("list agent session prompts: %w", err)
	}
	for _, prompt := range prompts {
		if text := strings.TrimSpace(prompt.Text); text != "" && !prompt.ObservedAt.Before(startedAt) {
			return text, nil
		}
	}
	return "", nil
}

// nameAgentSession names an agent session's current conversation from its
// first prompt. The agent Rig launched with its task's prompt takes the task's
// name; any other is named by its own provider, the way a task is.
func (o *taskObservation) nameAgentSession(
	ctx context.Context,
	task *Task,
	session AgentSession,
	prompt string,
) error {
	name := taskNameForPrompt(task, prompt)
	if name == "" {
		providerClient, err := supportedProviderClient(o.providers, session.Provider)
		if err != nil {
			return err
		}
		namingCtx, cancel := context.WithTimeout(ctx, agentNamingTimeout)
		defer cancel()
		suggestion, err := suggestTaskName(namingCtx, providerClient, task.RepoRoot, prompt)
		if err != nil {
			return err
		}
		name = suggestion.Name
	}
	return o.recordAgentSessionName(ctx, session, name)
}

// taskNameForPrompt returns the task's name when prompt is the prompt the
// task was created with, or "" otherwise. Rig pastes that prompt into the
// agent it launches, and the user may add to it before sending it.
func taskNameForPrompt(task *Task, prompt string) string {
	taskPrompt := strings.Join(strings.Fields(task.Prompt), " ")
	added, ok := strings.CutPrefix(strings.Join(strings.Fields(prompt), " "), taskPrompt)
	if taskPrompt == "" || !ok {
		return ""
	}
	// What the user added starts a new word, so a task prompt such as "fix"
	// does not take in "fixture".
	if next, _ := utf8.DecodeRuneInString(added); added != "" && (unicode.IsLetter(next) || unicode.IsDigit(next)) {
		return ""
	}
	return strings.TrimSpace(task.DisplayName)
}

// recordAgentSessionName stores the name of the conversation an agent session
// was named in, and publishes its task's status. An agent session that ended
// or moved to another conversation meanwhile is left as it is: that
// conversation is named on its own.
func (o *taskObservation) recordAgentSessionName(ctx context.Context, named AgentSession, name string) error {
	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	changes := agentSessionChanges{}
	defer o.publishAgentSessionChanges(ctx, changes)

	open, err := o.tasks.ListOpenAgentSessions(ctx, named.TaskID)
	if err != nil {
		return fmt.Errorf("list open agent sessions: %w", err)
	}
	index := slices.IndexFunc(open, func(session AgentSession) bool { return session.ID == named.ID })
	if index < 0 || open[index].ProviderSessionID != named.ProviderSessionID {
		return nil
	}
	session := open[index]
	session.Name = name
	session.NamedProviderSessionID = named.ProviderSessionID
	if err := o.tasks.UpdateAgentSession(ctx, session); err != nil {
		return fmt.Errorf("name agent session: %w", err)
	}
	changes[session.TaskID] = true
	return nil
}
