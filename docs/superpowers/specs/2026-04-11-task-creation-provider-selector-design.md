# Task Creation Provider Selector Styling

## Summary

Align the provider selector shown during task creation with the provider colors already used in the TUI control center overview.

The selector currently renders the selected provider with the generic primary text style and the unselected provider with the generic dim style. This causes the task-creation flow to diverge from the overview, where `codex` and `claude` each have distinct colors.

## Goals

- Reuse the existing provider color mapping from the control center overview.
- Make the selected provider visually distinct using bold text and an underline.
- Keep the unselected provider visible but slightly dimmed.
- Limit the scope to the task-creation flow.

## Non-Goals

- Changing provider colors anywhere else in the TUI.
- Introducing new provider color constants.
- Reworking the selector layout or interaction model.

## Design

### Rendering

The task-creation provider selector will continue to render both providers inline using the existing icon-plus-name label format.

- `codex` keeps the same provider color used in the overview.
- `claude` keeps the same provider color used in the overview.
- The selected provider adds `bold` and `underline`.
- The unselected provider uses the same provider color with a dimmed treatment.
- The separator remains neutral and dimmed.

### Source of Truth

The selector should derive its base colors from the existing `providerStyle(provider)` helper so the task-creation flow stays aligned with the control center overview automatically.

This avoids introducing duplicate selector-only color definitions that could drift from the overview.

### Scope of Code Changes

- Update the selector rendering helper in `internal/adapters/handler/cli/tui_model.go`.
- Reuse existing provider style primitives from `internal/adapters/handler/cli/tui_style.go`.
- Add focused tests in `internal/adapters/handler/cli/tui_model_test.go` that cover the selector output path.

## Testing

- Add a test that verifies the selected provider is rendered with bold and underline styling.
- Add a test that verifies the unselected provider still uses provider-specific styling instead of generic dim/primary styling.
- Keep the tests focused on the task-creation views that already render the selector.

## Risks

- ANSI-style assertions can become brittle if they depend on exact escape-sequence ordering.

To reduce that risk, tests should stay narrow and verify the presence of the expected rendered styling behavior rather than snapshotting large view bodies.
