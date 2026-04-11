# Add `gh` to doctor checks

## Summary

Add a `gh` CLI availability check to `agent doctor` that reports as a **note** (not a failure) when `gh` is missing, since it's an optional dependency used only for PR status checking.

## Design

### Interface change

Add `IsAvailable(ctx context.Context) error` to the `PRStatusChecker` interface in `internal/core/ports.go`. This matches the pattern used by every other adapter checked in doctor (`TaskRepository`, `RepoClient`, `SessionClient`, `ProviderClient`).

### Adapter implementation

In `internal/adapters/client/github/`, implement `IsAvailable` by running `gh --version`. Return an error if the command is not found or fails.

### Doctor integration

In `Service.Doctor()` (`internal/core/service.go`), if `s.prChecker` is non-nil, call `IsAvailable`. If it returns an error, append to `result.Notes` (not `result.Failures`), e.g.:

```
gh: gh CLI not found, PR status checks will be unavailable
```

Doctor still reports "ok" since notes don't constitute failures.

### Mock regeneration

Run `make generate` to regenerate mocks for the updated `PRStatusChecker` interface.

### Testing

- Unit test `IsAvailable` in `client/github/` (existing runner-mock pattern).
- Add doctor test cases in `service_doctor_test.go`: (1) `gh` available — no note added, (2) `gh` unavailable — note added, (3) `prChecker` not set — no note added.
