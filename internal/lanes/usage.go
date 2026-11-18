package lanes

// Usage is the token counts of one API call or one run.
type Usage struct {
	InputTokens         int64 `json:"input_tokens,omitempty"`
	OutputTokens        int64 `json:"output_tokens,omitempty"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	// Speed is what a call's usage says it ran at: "fast" for fast mode, which is priced above standard, and
	// "standard" or nothing otherwise.
	Speed string `json:"speed,omitempty"`
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
// A turn that stopped, or that a session ended in with no result, reports nothing, so the
// cost is a lower bound and UnknownTurns says by how many turns.
type CostMeter struct {
	// SessionTotalUSD is the last running total seen. A generation that resumes a
	// session starts from the value the one before it stopped at.
	SessionTotalUSD float64
	// CostUSD is what this generation added.
	CostUSD float64
	// UnknownTurns counts the turns whose cost is unknown: each turn that stopped, and the
	// turn a session crashed in or ended in with an input unanswered.
	UnknownTurns int64
	// KnownTurns counts the turns that reported a total, which is what the mean cost of a known turn is the mean
	// of. A stopped turn is charged at that mean.
	KnownTurns int64
}

// ObserveTurn takes one turn's running total and returns the turn's cost.
func (m *CostMeter) ObserveTurn(totalUSD float64) float64 {
	m.KnownTurns++
	cost := max(totalUSD-m.SessionTotalUSD, 0)
	m.SessionTotalUSD = max(m.SessionTotalUSD, totalUSD)
	m.CostUSD += cost
	return cost
}

// ObserveStopped counts a turn that reported no cost.
func (m *CostMeter) ObserveStopped() { m.UnknownTurns++ }
