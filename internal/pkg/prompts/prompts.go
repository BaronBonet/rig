package prompts

import _ "embed"

//go:embed suggest_task.md
var SuggestTaskPrompt string

// HandoffPrompt asks a session to write the note a fresh session of the same
// task starts from.
//
//go:embed handoff.md
var HandoffPrompt string
