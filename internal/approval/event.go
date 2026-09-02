package approval

import (
	"encoding/json"
)

// FromEvent converts an agent approval payload into the store shape. JSON
// round-trip keeps transport payloads decoupled from the HTTP layer.
func FromEvent(sessionID string, data map[string]any) PendingApproval {
	var raw struct {
		ID          string   `json:"approval_id"`
		Command     string   `json:"command"`
		Description string   `json:"description"`
		PatternKeys []string `json:"pattern_keys"`
		RunID       string   `json:"run_id"`
	}
	b, _ := json.Marshal(data)
	_ = json.Unmarshal(b, &raw)
	return PendingApproval{
		ID: raw.ID, SessionID: sessionID, Command: raw.Command,
		Description: raw.Description, PatternKeys: raw.PatternKeys, RunID: raw.RunID,
	}
}
