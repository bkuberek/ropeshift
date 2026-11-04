package lanes

// Usage is the token counts of one API call or one run.
type Usage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens,omitempty"`
}

// Context is what a session holds at one API call: every token sent, whether new,
// written to the cache, or read from it. A run's total is never context, because it
// sums the calls of a turn.
func (u Usage) Context() int64 {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens
}

// Add sums two runs.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens + other.InputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
	}
}

// CostMeter turns a session's running total into a generation's cost. A vendor
// reports one total that accumulates across resumes, so a turn's cost is a difference.
// A stopped turn reports nothing, so the cost is a lower bound and UnknownTurns says
// by how many turns.
type CostMeter struct {
	// SessionTotalUSD is the last running total seen. A generation that resumes a
	// session starts from the value the one before it stopped at.
	SessionTotalUSD float64
	// CostUSD is what this generation added.
	CostUSD float64
	// UnknownTurns counts the stopped turns whose cost is unknown.
	UnknownTurns int64
}

// ObserveTurn takes one turn's running total and returns the turn's cost.
func (m *CostMeter) ObserveTurn(totalUSD float64) float64 {
	cost := max(totalUSD-m.SessionTotalUSD, 0)
	m.SessionTotalUSD = max(m.SessionTotalUSD, totalUSD)
	m.CostUSD += cost
	return cost
}

// ObserveStopped counts a turn that reported no cost.
func (m *CostMeter) ObserveStopped() { m.UnknownTurns++ }
