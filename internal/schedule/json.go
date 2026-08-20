package schedule

import "encoding/json"

// jsonMarshal is a tiny indirection over encoding/json.Marshal so the main
// schedule.go does not need to import encoding/json directly (keeps the
// imports list focused on the domain types).
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
