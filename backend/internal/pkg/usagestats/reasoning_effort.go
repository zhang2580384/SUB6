package usagestats

import (
	"fmt"
	"strings"
)

// ParseReasoningEffortFilter uses the requested effort shown in usage records.
// "unknown" matches records without an effort; "none" is an explicit request.
func ParseReasoningEffortFilter(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	if value == "extrahigh" {
		value = "xhigh"
	}
	switch value {
	case "", "unknown", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return value, nil
	default:
		return "", fmt.Errorf("invalid reasoning_effort: use none, minimal, low, medium, high, xhigh, max or unknown")
	}
}
