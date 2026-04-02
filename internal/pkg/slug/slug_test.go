package slug

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromDisplayName_NormalizesToLowerKebabCase(t *testing.T) {
	got := FromDisplayName("Billing Retry Flow")
	require.Equal(t, "billing-retry-flow", got)
}

func TestEnsureUnique_AppendsNumericSuffix(t *testing.T) {
	got := EnsureUnique("billing-retry-flow", map[string]struct{}{
		"billing-retry-flow":   {},
		"billing-retry-flow-2": {},
	})

	require.Equal(t, "billing-retry-flow-3", got)
}
