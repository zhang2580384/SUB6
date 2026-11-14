package usagestats

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParseReasoningEffortFilter(t *testing.T) {
	for raw, want := range map[string]string{"": "", "none": "none", "minimal": "minimal", "low": "low", "medium": "medium", "HIGH": "high", "xhigh": "xhigh", "max": "max", "unknown": "unknown", " X-High ": "xhigh", "extra_high": "xhigh"} {
		got, err := ParseReasoningEffortFilter(raw)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, raw := range []string{"unsupported", "high,low", "high' OR 1=1 --"} {
		_, err := ParseReasoningEffortFilter(raw)
		require.Error(t, err)
	}
}
