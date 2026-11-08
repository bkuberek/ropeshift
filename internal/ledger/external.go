package ledger

import "fmt"

// ExternalKey names a record in the system it came from (FR-LEDGER-5). Native IDs
// and display keys differ across products, so all three parts identify the record.
type ExternalKey struct {
	Adapter  string `json:"adapter"`
	Instance string `json:"instance"`
	NativeID string `json:"native_id"`
}

func (k ExternalKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Adapter, k.Instance, k.NativeID)
}

// ExternalLink ties a ledger record to its external key.
type ExternalLink struct {
	RecordKind string      `json:"record_kind"`
	RecordID   string      `json:"record_id"`
	Key        ExternalKey `json:"key"`
	DisplayKey string      `json:"display_key,omitempty"`
}
