package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

//go:embed telemetry_migrations/*.sql
var telemetryFiles embed.FS

// defaultTelemetryLimit bounds a query that names no limit, so a person's first query cannot print
// a week of data.
const defaultTelemetryLimit = 100

// The agent's own names the lane summary reads when ropeshift recorded nothing of a lane. They are
// Claude Code's, and the store reads them only to sum them, never to rewrite them.
const (
	agentAPIRequest = "claude_code.api_request"
	agentTokens     = "claude_code.token.usage"
	agentCost       = "claude_code.cost.usage"
	agentTokenType  = "type"
)

// retentionStep is the fewest rows one pass of the size bound deletes, so a store far over its
// bound does not take a statement per row.
const retentionStep = 100

// TelemetryStore is the worker's local telemetry store: a SQLite file of its own, apart from the
// journal and the ledger, so it keeps its own failure domain and retention.
type TelemetryStore struct {
	db *sql.DB
}

// OpenTelemetry opens a telemetry file.
func OpenTelemetry(file string) (*TelemetryStore, error) {
	db, err := OpenDB(file)
	if err != nil {
		return nil, err
	}
	return &TelemetryStore{db: db}, nil
}

// OpenMemoryTelemetry opens a telemetry store on a new in-memory database.
func OpenMemoryTelemetry() (*TelemetryStore, error) {
	db, err := OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	return &TelemetryStore{db: db}, nil
}

// Close closes the database.
func (s *TelemetryStore) Close() error { return s.db.Close() }

// Migrate applies each schema file once.
func (s *TelemetryStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS telemetry_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	names, err := fs.Glob(telemetryFiles, "telemetry_migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, name := range names {
		if err := s.applyMigration(ctx, name); err != nil {
			return fmt.Errorf("telemetry migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *TelemetryStore) applyMigration(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM telemetry_migrations WHERE name = ?1`, name).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	text, err := fs.ReadFile(telemetryFiles, name)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(text)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO telemetry_migrations (name) VALUES (?1)`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// labelOf is the token type a record counts under, which the lane summary sums by. An agent's tokens count
// under the agent and the type, as `<agent>:<type>`, because an agent's name holds no colon.
func labelOf(record ports.TelemetryRecord) string {
	if record.Name == ledger.MetricAgentTokens {
		agent, _ := record.Attributes[ledger.AttrAgent].(string)
		kind, _ := record.Attributes[ledger.AttrTokenType].(string)
		return agent + ":" + kind
	}
	for _, key := range []string{ledger.AttrTokenType, agentTokenType} {
		if text, ok := record.Attributes[key].(string); ok && (record.Name == ledger.MetricTokens || record.Name == agentTokens) {
			return text
		}
	}
	return ""
}

// Append keeps one batch in one transaction, so a batch is in the store whole or not at all.
func (s *TelemetryStore) Append(ctx context.Context, receivedAt int64, records []ports.TelemetryRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.PrepareContext(ctx, `INSERT INTO telemetry_records
  (received_at, signal, name, lane_id, work_order_id, generation, observed_at, value, label, attributes, detail)
  VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, record := range records {
		attributes, err := json.Marshal(orEmpty(record.Attributes))
		if err != nil {
			return err
		}
		detail, err := json.Marshal(orEmpty(record.Detail))
		if err != nil {
			return err
		}
		var v sql.NullFloat64
		if record.Value != nil {
			v = sql.NullFloat64{Float64: *record.Value, Valid: true}
		}
		_, err = statement.ExecContext(ctx, receivedAt, record.Signal, record.Name, record.Lane, record.WorkOrder, record.Generation,
			record.ObservedAt, v, labelOf(record), string(attributes), string(detail))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Records reads what a query selects, newest first.
func (s *TelemetryStore) Records(ctx context.Context, query ports.TelemetryQuery) ([]ports.TelemetryRecord, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = defaultTelemetryLimit
	}
	var where []string
	args := []any{}
	add := func(clause string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if query.Signal != "" {
		add("signal = ?%d", query.Signal)
	}
	if query.Name != "" {
		add("name = ?%d", query.Name)
	}
	if query.Lane != "" {
		add("lane_id = ?%d", query.Lane)
	}
	if query.Since != 0 {
		add("observed_at >= ?%d", query.Since)
	}
	text := `SELECT id, received_at, signal, name, lane_id, work_order_id, generation, observed_at, value, attributes, detail FROM telemetry_records`
	if len(where) > 0 {
		text += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	text += fmt.Sprintf(" ORDER BY observed_at DESC, id DESC LIMIT ?%d", len(args))
	rows, err := s.db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.TelemetryRecord
	for rows.Next() {
		var record ports.TelemetryRecord
		var v sql.NullFloat64
		var attributes, detail string
		if err := rows.Scan(&record.ID, &record.ReceivedAt, &record.Signal, &record.Name, &record.Lane, &record.WorkOrder,
			&record.Generation, &record.ObservedAt, &v, &attributes, &detail); err != nil {
			return nil, err
		}
		if v.Valid {
			record.Value = &v.Float64
		}
		if err := json.Unmarshal([]byte(attributes), &record.Attributes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(detail), &record.Detail); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// laneFigures is what the summary gathers of one lane from one source.
type laneFigures struct {
	tokens map[string]int64
	cost   float64
}

// Lanes sums what the store holds of each lane. Context is the newest reading from either source,
// because both report the same call. Tokens and cost come from ropeshift's own metrics, which the
// worker took from the driver, and from the agent's telemetry only for a lane ropeshift said nothing
// of, so nothing counts twice.
func (s *TelemetryStore) Lanes(ctx context.Context) ([]ports.LaneTelemetry, error) {
	summaries := map[string]*ports.LaneTelemetry{}
	get := func(lane string) *ports.LaneTelemetry {
		if summaries[lane] == nil {
			summaries[lane] = &ports.LaneTelemetry{Lane: lane, Tokens: map[string]int64{}}
		}
		return summaries[lane]
	}
	if err := s.lastSeen(ctx, get); err != nil {
		return nil, err
	}
	if err := s.context(ctx, get); err != nil {
		return nil, err
	}
	own, err := s.figures(ctx, ledger.MetricTokens, ledger.MetricCost)
	if err != nil {
		return nil, err
	}
	agent, err := s.figures(ctx, agentTokens, agentCost)
	if err != nil {
		return nil, err
	}
	byAgent, err := s.figures(ctx, ledger.MetricAgentTokens, "")
	if err != nil {
		return nil, err
	}
	for lane, figures := range byAgent {
		summary := get(lane)
		for label, tokens := range figures.tokens {
			agent, kind, _ := strings.Cut(label, ":")
			if summary.Agents == nil {
				summary.Agents = map[string]map[string]int64{}
			}
			if summary.Agents[agent] == nil {
				summary.Agents[agent] = map[string]int64{}
			}
			summary.Agents[agent][kind] += tokens
		}
	}
	for lane := range summaries {
		figures, ok := own[lane]
		if !ok {
			figures, ok = agent[lane]
		}
		if !ok {
			continue
		}
		summary := summaries[lane]
		summary.Tokens, summary.CostUSD = figures.tokens, figures.cost
	}
	out := make([]ports.LaneTelemetry, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, *summary)
	}
	slices.SortFunc(out, func(a, b ports.LaneTelemetry) int { return strings.Compare(a.Lane, b.Lane) })
	return out, nil
}

func (s *TelemetryStore) lastSeen(ctx context.Context, get func(string) *ports.LaneTelemetry) error {
	rows, err := s.db.QueryContext(ctx, `SELECT lane_id, MAX(received_at) FROM telemetry_records WHERE lane_id != '' GROUP BY lane_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lane string
		var at int64
		if err := rows.Scan(&lane, &at); err != nil {
			return err
		}
		get(lane).LastAt = at
	}
	return rows.Err()
}

func (s *TelemetryStore) context(ctx context.Context, get func(string) *ports.LaneTelemetry) error {
	rows, err := s.db.QueryContext(ctx, `SELECT lane_id, value, observed_at FROM telemetry_records
 WHERE name IN (?1, ?2) AND lane_id != '' AND value IS NOT NULL ORDER BY observed_at, id`, ledger.MetricContextTokens, agentAPIRequest)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lane string
		var tokens float64
		var at int64
		if err := rows.Scan(&lane, &tokens, &at); err != nil {
			return err
		}
		summary := get(lane)
		summary.ContextTokens, summary.ContextAt = int64(tokens), at
	}
	return rows.Err()
}

// figures sums one source's tokens and cost per lane. A delta series sums, and a cumulative one
// counts its last reading, which is its total.
func (s *TelemetryStore) figures(ctx context.Context, tokens, cost string) (map[string]laneFigures, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lane_id, name, label, value, COALESCE(json_extract(detail, '$.temporality'), '')
  FROM telemetry_records WHERE name IN (?1, ?2) AND lane_id != '' AND value IS NOT NULL ORDER BY observed_at, id`, tokens, cost)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type series struct{ lane, name, label string }
	sums, last := map[series]float64{}, map[series]float64{}
	cumulative := map[series]bool{}
	for rows.Next() {
		var key series
		var v float64
		var temporality string
		if err := rows.Scan(&key.lane, &key.name, &key.label, &v, &temporality); err != nil {
			return nil, err
		}
		sums[key] += v
		last[key] = v
		cumulative[key] = cumulative[key] || temporality == "cumulative"
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]laneFigures{}
	for key, sum := range sums {
		if cumulative[key] {
			sum = last[key]
		}
		figures := out[key.lane]
		if figures.tokens == nil {
			figures.tokens = map[string]int64{}
		}
		if key.name == cost {
			figures.cost += sum
		} else {
			figures.tokens[key.label] += int64(sum)
		}
		out[key.lane] = figures
	}
	return out, nil
}

// Size is how much of the file holds data: its pages, less those on the free list, which the next
// write reuses.
func (s *TelemetryStore) Size(ctx context.Context) (int64, error) {
	var pages, free, pageSize int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, err
	}
	return (pages - free) * pageSize, nil
}

// Retain holds the store to its bounds by age and by size, whichever comes first (D3). The oldest
// records go first, by when they arrived.
func (s *TelemetryStore) Retain(ctx context.Context, now int64, maxAge time.Duration, maxBytes int64) (int64, error) {
	var deleted int64
	if maxAge > 0 {
		result, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_records WHERE received_at < ?1`, now-maxAge.Milliseconds())
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	if maxBytes <= 0 {
		return deleted, nil
	}
	for {
		size, err := s.Size(ctx)
		if err != nil {
			return deleted, err
		}
		if size <= maxBytes {
			return deleted, nil
		}
		n, err := s.dropOldest(ctx)
		if err != nil {
			return deleted, err
		}
		if n == 0 {
			return deleted, nil
		}
		deleted += n
	}
}

// dropOldest deletes a tenth of the store, or retentionStep rows when that is more.
func (s *TelemetryStore) dropOldest(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM telemetry_records`).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	step := max(count/10, retentionStep)
	result, err := s.db.ExecContext(ctx, `DELETE FROM telemetry_records WHERE id IN
  (SELECT id FROM telemetry_records ORDER BY received_at, id LIMIT ?1)`, step)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

var _ ports.TelemetryReader = (*TelemetryStore)(nil)
