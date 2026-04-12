# Background Task Creation Design

## Summary

Replace the blocking new-task modal flow with a single background creation flow that returns to the main TUI immediately after prompt submission. While creation is in flight, the task list shows a temporary selected row titled `Creating task...` that updates through creation steps and later becomes the real task row once the service has created it.

This change removes the name-confirmation step entirely. The suggested name is accepted automatically and updates the row in place when it becomes available.

## Goals

- Return to the main list immediately after submitting a new task prompt.
- Keep the TUI interactive while one task is being created in the background.
- Show creation progress inside the list/detail layout instead of a blocking modal.
- Preserve visibility into failures by keeping the creating row visible and surfacing errors in the detail pane.

## Non-Goals

- Supporting multiple simultaneous task creations.
- Keeping the name-confirmation screen as an optional branch.
- Reworking core task creation orchestration beyond what is needed to expose progress cleanly to the TUI.

## Product Decisions

- Only one new task may be created at a time.
- Prompt submission starts creation immediately.
- The selection jumps to the temporary creating row so progress is visible without extra navigation.
- The temporary row title starts as `Creating task...`.
- The title updates in place to the suggested task name when naming completes.
- The temporary row remains visible through failures instead of disappearing abruptly.

## UX Flow

1. User presses `n` to open the prompt input.
2. User types a prompt and presses `Enter`.
3. Prompt input closes immediately and the model returns to list mode.
4. A new selected temporary row appears with:
   - Title: `Creating task...`
   - Status: `creating`
   - Provider: selected provider
   - Neutral PR state
5. The detail pane shows creation progress with completed steps checkmarked and the active step animated.
6. When naming completes, the row title changes to the suggested name.
7. When task creation succeeds, the temporary row becomes the real task row without losing selection.
8. If creation fails before or after persistence, the row remains visible and the detail pane shows the failure.

## Rendering Model

The list view continues to render persisted `taskViews`, but may also render one synthetic task row derived from an in-flight creation state.

The synthetic row should:

- Render in the same two-line task-list format as persisted rows.
- Use the agreed provisional title until the suggested name is known.
- Show `creating` as the visible status.
- Omit PR metadata until the real task exists.
- Participate in selection logic as if it were a normal row.

The detail pane becomes the main creation-progress surface for this row. It shows:

- The submitted prompt.
- The provider.
- Completed progress steps with checkmarks.
- The active progress step with shimmer/animated indicator.
- Any terminal error if creation fails.

## State Model

The current `busy` flag should no longer be the mechanism that freezes the entire TUI during task creation. Creation is already asynchronous at the command level; the blocking behavior comes from global input suppression.

Introduce a dedicated in-flight creation state in the TUI model, for example:

- submitted prompt
- selected provider
- provisional display name
- current progress step
- ordered progress step history
- current task snapshot, if one has been emitted
- terminal error, if one occurs
- whether the synthetic row has transitioned to a real persisted task

This state powers synthetic-row rendering and creation-specific detail rendering while the rest of the list remains interactive.

## Control Flow Changes

### Prompt Submission

Current behavior:

- `Enter` in prompt mode starts name suggestion.
- TUI remains in modal flow.
- Global `busy` blocks all input.
- Name-confirm mode appears before actual creation.

New behavior:

- `Enter` in prompt mode starts `CreateTaskWithProgress` immediately.
- Prompt mode exits to list mode right away.
- The model initializes the in-flight creation state and selects the synthetic row.
- Name suggestion happens inside the background create flow.

### Name Confirmation

The name-confirmation mode is removed. The suggested name is accepted automatically.

### List Interaction During Creation

While creation is in flight:

- Arrow-key navigation remains available.
- Refresh remains available.
- Opening or cleaning up the creating row is disabled.
- Pressing `n` does not open a second create flow and instead surfaces an inline error such as `Task creation already in progress`.

## Service and Progress Requirements

The existing `CreateTaskWithProgress` flow is close to what is needed, but the TUI depends on progress updates carrying enough state to render the transition cleanly.

Required properties:

- Progress events must expose the selected name when naming completes.
- Progress events should expose a task snapshot as soon as a task identity exists.
- The final completion event must provide the created task or an error.

This lets the TUI:

- Update the row title when naming completes.
- Swap from synthetic to persisted row once the task has been created in storage.
- Keep rendering progress even while subsequent setup steps continue.

## Failure Handling

### Failure Before Task Persistence

If failure occurs before `CreateTask` succeeds:

- Keep the synthetic row visible.
- Mark it as failed in the detail pane.
- Preserve the submitted prompt and last completed step.
- Allow the user to move away and back to inspect the error.

### Failure After Task Persistence

If failure occurs after the task has been created:

- Preserve the real task row.
- Use the service-returned status and error message.
- Let normal persisted-task rendering take over once the real task exists.

### Async Safety

If the background command panics or a progress stream closes unexpectedly:

- Convert that into visible creation failure state.
- Do not silently drop the temporary row.

## Data/Rendering Integration

The list model will need a stable way to combine persisted rows with the temporary creating row.

Recommended approach:

- Keep persisted `taskViews` as the source of truth for normal tasks.
- Derive a synthetic `TaskView`-like structure from the in-flight creation state for rendering only.
- Insert the synthetic row at the end of the visible list while it has not yet been replaced by a persisted task.
- Once the real persisted task is available, prefer the real row but retain selection on that logical task.

This avoids polluting repository-backed task state with UI-only placeholders.

## Testing Strategy

Add or update TUI tests for:

- Prompt submission returns to list mode immediately.
- A selected temporary row appears with title `Creating task...`.
- The row title updates when the suggested name arrives.
- Progress steps render in the detail pane while list navigation still works.
- Pressing `n` during creation shows the single-in-flight error.
- Open/cleanup actions are disabled for the creating row.
- Successful completion replaces the temporary row with the persisted row without losing selection.
- Failure leaves a visible row and visible error.
- The old modal footer hints for submit/create are no longer part of the creation path after prompt submission.

## Risks

- Synthetic-row insertion can create selection bugs if it is keyed by index instead of logical identity.
- Background progress can be lost if refresh logic blindly rebuilds the visible list and discards UI-only state.
- Failure handling before persistence needs explicit design so users do not see rows vanish unexpectedly.

## Recommended Implementation Slice

1. Add in-flight creation state to the TUI model.
2. Change prompt submission to start background creation and return to list mode.
3. Remove the name-confirm mode from the create flow.
4. Render the synthetic creating row and creation-specific detail pane.
5. Decouple task creation from the global `busy` input lock.
6. Enforce single in-flight creation with a user-visible inline error.
7. Add tests for success, progress, navigation, and failure cases.
