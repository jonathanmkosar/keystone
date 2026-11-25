package main

// Compaction (P5-02) keeps the summaries of a chat's oldest turns, and a
// summary of a chat that was sent sensitive content goes only to a call
// whose every model may read it: compact.Usable takes the model's plain
// answer. The package enforces rule 3 beside the registry and routing.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/compact")
}
