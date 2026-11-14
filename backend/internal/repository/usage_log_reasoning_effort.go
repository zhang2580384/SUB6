package repository

import "fmt"

// Match the requested effort used by the usage DTO, falling back to the stored
// forwarded effort for legacy rows. Never infer it from the current group policy.
func appendUsageLogReasoningEffortCondition(conditions []string, args []any, effort, prefix string) ([]string, []any) {
	if effort == "" {
		return conditions, args
	}
	// Match the DTO's whitespace trimming, including old records with blank
	// requested values that must fall back to the effective value.
	const trimChars = `E' \t\n\r\f' || CHR(11)`
	requested := "BTRIM(" + prefix + "requested_reasoning_effort, " + trimChars + ")"
	column := "LOWER(BTRIM(COALESCE(NULLIF(" + requested + ", ''), " + prefix + "reasoning_effort, ''), " + trimChars + "))"
	if effort == "unknown" {
		return append(conditions, column+" = ''"), args
	}
	if effort == "xhigh" {
		column = "REPLACE(REPLACE(REPLACE(" + column + ", '-', ''), '_', ''), ' ', '')"
		conditions = append(conditions, fmt.Sprintf("%s IN ($%d, 'extrahigh')", column, len(args)+1))
	} else {
		conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)+1))
	}
	return conditions, append(args, effort)
}

func appendUsageLogReasoningEffortQuery(query string, args []any, effort, prefix string) (string, []any) {
	conditions, args := appendUsageLogReasoningEffortCondition(nil, args, effort, prefix)
	for _, condition := range conditions {
		query += " AND " + condition
	}
	return query, args
}
