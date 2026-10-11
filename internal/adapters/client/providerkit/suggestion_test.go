package providerkit

import (
	"context"
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestParseSuggestion_ReadsTicketReference(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want core.TaskSuggestion
	}{
		{
			name: "ticket and name",
			raw:  `{"branch_type": "fix", "ticket": "GH #18", "name": "Fix the broken thing"}`,
			want: core.TaskSuggestion{Name: "Fix the broken thing", BranchType: "fix", Ticket: "GH #18"},
		},
		{
			name: "ticket without a name",
			raw:  `{"branch_type": "feat", "ticket": "ABC-123", "name": ""}`,
			want: core.TaskSuggestion{BranchType: "feat", Ticket: "ABC-123"},
		},
		{
			name: "decorated ticket",
			raw:  "{\"ticket\": \"`[sc-1234]`\", \"name\": \"Retry billing\"}",
			want: core.TaskSuggestion{Name: "Retry billing", BranchType: "feat", Ticket: "sc-1234"},
		},
		{
			name: "pretty-printed suggestion after commentary",
			raw: "Looked up GH #18.\n```json\n{\n  \"branch_type\": \"fix\",\n  \"ticket\": \"GH #18\",\n" +
				"  \"name\": \"Fix the broken thing\"\n}\n```",
			want: core.TaskSuggestion{Name: "Fix the broken thing", BranchType: "fix", Ticket: "GH #18"},
		},
		{
			name: "placeholder ticket",
			raw:  `{"branch_type": "feat", "ticket": "N/A", "name": "Retry billing"}`,
			want: core.TaskSuggestion{Name: "Retry billing", BranchType: "feat"},
		},
		{
			name: "URL instead of a ticket reference",
			raw:  `{"ticket": "https://github.com/acme/other/issues/5", "name": "Retry billing"}`,
			want: core.TaskSuggestion{Name: "Retry billing", BranchType: "feat"},
		},
		{
			name: "issue of another repository",
			raw:  `{"ticket": "acme/other#5", "name": "Retry billing"}`,
			want: core.TaskSuggestion{Name: "Retry billing", BranchType: "feat", Ticket: "acme/other#5"},
		},
		{
			name: "suggestion with neither is skipped for an earlier line",
			raw:  "{\"ticket\": \"#18\", \"name\": \"Fix flaky test\"}\n{\"branch_type\": \"feat\", \"ticket\": \"\", \"name\": \"\"}",
			want: core.TaskSuggestion{Name: "Fix flaky test", BranchType: "feat", Ticket: "#18"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestion, ok := ParseSuggestion(tt.raw, nil)

			require.True(t, ok)
			require.Equal(t, tt.want, suggestion)
		})
	}
}

func TestSuggestWithTicketLookup_DoesNotFallBackOnceCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0

	_, err := SuggestWithTicketLookup(ctx, func(context.Context, bool) (core.TaskSuggestion, error) {
		attempts++
		cancel()
		return core.TaskSuggestion{}, errors.New("interrupted")
	})

	require.EqualError(t, err, "interrupted")
	require.Equal(t, 1, attempts)
}
