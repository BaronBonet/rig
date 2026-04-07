# Codex Hooks Observability Spike

**Date:** 2026-04-07
**Status:** Approved

## Summary

Add an in-repo spike to inspect what structured runtime information Codex hooks expose for normal Codex sessions launched with this project's hook configuration.

The spike should not attempt to redesign the current tmux-backed runtime model yet. Its job is narrower: collect raw hook payloads, preserve them across restarts in a log file, and make them easy to inspect so we can decide whether hooks are rich enough to drive better TUI state than the current `running` / `needs_input` badges.

## Goals

- Determine which Codex hook event types are emitted during a normal managed session.
- Preserve the exact raw hook payloads for later analysis.
- Verify whether hook payloads expose stable identifiers suitable for mapping back to managed tasks or sessions.
- Verify whether hook payloads expose enough semantic milestones to support richer TUI state.
- Keep the spike close to the likely long-term architecture without touching product code prematurely.

## Non-Goals

- No SQLite persistence in this spike.
- No TUI integration in this spike.
- No changes to the current tmux runtime monitor or Codex tmux detector.
- No attempt to derive final runtime-state enums in this spike.
- No redesign of `agent` task storage or service boundaries in this spike.
- No support for observing arbitrary unmanaged Codex sessions that are not launched with this project's hook configuration.

## Why Hooks First

The project goal is a restartable TUI for managing LLM tasks where a human also interacts with each task. The current tmux-based approach can infer basic live state, but it relies on parsing terminal output and foreground-process heuristics.

Hooks are a better first source for richer observability because they come from Codex's own lifecycle rather than from a terminal rendering layer. They should give us more authoritative semantic boundaries such as session start, prompt submission, tool execution, and stop events.

This does not assume hooks are sufficient for every future UI detail. The spike exists precisely to answer that question using real payloads instead of assumptions.

## Recommended Approach

Build a file-backed hook collector spike inside this repository.

The spike consists of:

- a small local HTTP collector command
- a small log-reader command
- a repo-local hook forwarding script and Codex hook configuration

Each configured Codex hook forwards its raw JSON payload to the collector. The collector appends one record per line to a JSONL log file. A separate reader command summarizes the captured events for human inspection.

This is preferred over a websocket-only spike because the log survives restarts and supports offline inspection. It is preferred over SQLite persistence because the spike's purpose is to inspect raw hook data, not to finalize a storage schema.

The collector should write to a repo-local log file so the spike remains self-contained. The default path should be `.agent/observability/codex-hooks.jsonl`, and the directory should be created automatically if it does not exist.

## Architecture

### 1. Collector

Add a small command at `cmd/hook-collector`.

Responsibilities:

- listen on loopback HTTP only
- accept POST requests from the hook forwarding script
- append each received event to `.agent/observability/codex-hooks.jsonl`
- add only a minimal collector envelope around the raw payload
- avoid mutating or normalizing the raw Codex payload

The collector is a passive sink. It does not attempt to interpret events, maintain session state, or talk back to Codex.

### 2. Hook Forwarder

Add a small repo-local forwarding script invoked by Codex hooks.

Responsibilities:

- read the raw hook payload from stdin
- know which configured hook event triggered the invocation
- POST the payload to the collector
- leave the original payload unchanged

The forwarder should stay intentionally dumb. The collector owns durability; later product code can own normalization.

### 3. Log Reader

Add a small command at `cmd/hook-log`.

Responsibilities:

- read the JSONL log file
- print a readable summary grouped by event type and likely session identifiers
- allow direct inspection of the raw payloads when needed

The reader exists only to help evaluate the hook contract quickly during the spike.

## Hook Events To Capture

The spike should capture at least these Codex hook events because they are the most likely to matter for future TUI state:

- `SessionStart`
- `UserPromptSubmit`
- `PreToolUse`
- `PostToolUse`
- `Stop`

If the installed Codex version exposes additional relevant hook events, they may be added to the spike, but the spike should not depend on any event outside this minimum set.

## Log Format

Each line in the log file should be a single JSON object with a small collector envelope and the unchanged raw payload.

Required envelope fields:

- `received_at`
- `event_name`
- `raw_payload`

Optional envelope fields:

- `remote_addr`
- `request_path`
- `parse_error`

Rules:

- `raw_payload` must be stored exactly as received from Codex
- if the body is valid JSON, store it as raw JSON data without rewriting fields
- if the body is not valid JSON, store the raw text and record that explicitly
- do not rename, flatten, or normalize Codex fields in this spike

This keeps the spike honest and preserves the original hook contract for later design work.

## Questions The Spike Must Answer

The spike is successful only if it gives clear evidence for these questions:

1. Do hook payloads expose stable identifiers we can map to a managed task or session?
2. Do hook payloads expose enough semantic milestones to support richer TUI state than `running` / `needs_input`?
3. Do hook payloads include any useful human-facing text that could later be surfaced directly in the TUI?
4. Which desired UI details are still missing from hooks and would require tmux fallback or another source?

## Expected Evaluation Outcome

After running the spike against real Codex sessions, we should be able to classify future observability work into one of three buckets:

- hooks are sufficient for most TUI state and tmux becomes a narrow fallback
- hooks are useful for lifecycle milestones but tmux remains necessary for some live display details
- hooks are too sparse for the intended product and the current tmux model remains primary

The spike is explicitly meant to support that decision.

## Verification

Verification should use a real Codex session configured with the repo-owned forwarding hooks.

Minimum verification flow:

1. start the collector
2. run a Codex session with this project's hook configuration enabled
3. submit at least one user prompt
4. trigger at least one tool execution
5. stop the session
6. inspect the resulting JSONL log with the reader command

The result should show which hook events were emitted, in what order, and what raw fields Codex actually provided.

## Scope Boundaries

- The spike is not part of the user-facing `agent` product yet.
- The spike does not change current `agent tui` behavior.
- The spike does not define the eventual SQLite schema.
- The spike does not define the eventual runtime-state model.
- The spike does not require app-server integration.

## Follow-Up If The Spike Succeeds

If the spike shows that hooks expose useful stable identifiers and semantic milestones, the next design phase should answer:

- how to map hook events onto existing managed tasks
- what should be persisted into SQLite versus recomputed live
- which UI states can become hook-driven
- which details still require tmux-derived fallback

That later design should happen only after inspecting real captured payloads from this spike.
