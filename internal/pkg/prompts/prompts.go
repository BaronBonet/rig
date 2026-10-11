package prompts

import (
	_ "embed"
	"strings"
)

//go:embed suggest_task.md
var suggestTaskTemplate string

const ticketLookupPlaceholder = "{{TICKET_LOOKUP}}"

const ticketLookupInstructions = "When the task is to work on a specific issue or ticket, look it up first " +
	"so the title can say what it is about. Use only read-only tools you already have, such as " +
	"`gh issue view <number or URL>` for GitHub issues or an issue tracker's MCP tools. Never create, edit, " +
	"comment on, assign or transition anything. If a couple of attempts do not find it, name the task " +
	"from the description alone."

const noToolsInstructions = "Do not use any tools. Name the task from the description alone."

// SuggestTask builds the task naming prompt for a task description. With
// lookupTickets, the provider is asked to read the issue or ticket the task
// works on through its own read-only tools, so the name can describe it.
func SuggestTask(description string, lookupTickets bool) string {
	instructions := noToolsInstructions
	if lookupTickets {
		instructions = ticketLookupInstructions
	}

	prompt := strings.Replace(suggestTaskTemplate, ticketLookupPlaceholder, instructions, 1)
	return prompt + "\n\nTask description: " + description
}
