package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// packTx is the context pack area of one transaction. No statement here has a limit, because a
// read that stopped at a page would leave a lane untold.
type packTx struct {
	*session
	scope ledger.WorkspaceID
}

const packColumns = `id, version, work_order_id, digest, size_estimate, body`

var (
	stmtInsertPack = define("pack.insert",
		`INSERT INTO context_packs (workspace_id, id, version, work_order_id, digest, size_estimate, body, created_at)
VALUES (:workspace_id, :id, :version, :work_order_id, :digest, :tokens_estimate, :body, :now)`)

	stmtInsertPackItem = define("pack.insert_item",
		`INSERT INTO context_pack_items (workspace_id, pack_id, version, position, kind, ref, record_id, digest, trust, indexed)
VALUES (:workspace_id, :pack_id, :version, :position, :kind, :ref, :record_id, :digest, :trust, :indexed)`)

	stmtReadPack = define("pack.read",
		`SELECT `+packColumns+` FROM context_packs WHERE workspace_id = :workspace_id AND id = :id AND version = :version`)

	stmtLatestPack = define("pack.latest",
		`SELECT `+packColumns+` FROM context_packs
 WHERE workspace_id = :workspace_id AND id = :id
 ORDER BY version DESC
 LIMIT 1`)

	stmtAllPacks = define("pack.all",
		`SELECT `+packColumns+` FROM context_packs WHERE workspace_id = :workspace_id ORDER BY id, version`)

	stmtPackItems = define("pack.items",
		`SELECT kind, ref, digest, trust, indexed FROM context_pack_items
 WHERE workspace_id = :workspace_id AND pack_id = :pack_id AND version = :version
 ORDER BY position`)

	// stmtPinning reads the index. A record a pack dropped in a later version pins that pack no more.
	stmtPinning = define("pack.pinning",
		`SELECT DISTINCT i.pack_id
  FROM context_pack_items i
 WHERE i.workspace_id = :workspace_id AND i.kind = :kind AND i.record_id = :record_id
   AND i.version = (SELECT max(p.version) FROM context_packs p WHERE p.workspace_id = i.workspace_id AND p.id = i.pack_id)
 ORDER BY i.pack_id`)

	stmtPackLanes = define("pack.lanes",
		`SELECT DISTINCT lane_id FROM claims
 WHERE workspace_id = :workspace_id AND pack_id = :pack_id AND lane_id IS NOT NULL
 ORDER BY lane_id`)

	stmtAdditions = define("pack.additions",
		`SELECT work_order_id, kind, ref, body, added_by, added_at FROM context_pack_additions
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY added_at, kind, ref`)

	stmtAllAdditions = define("pack.all_additions",
		`SELECT work_order_id, kind, ref, body, added_by, added_at FROM context_pack_additions
 WHERE workspace_id = :workspace_id
 ORDER BY added_at, kind, ref`)

	// stmtAddItem keeps the first word on one record.
	stmtAddItem = define("pack.add",
		`INSERT INTO context_pack_additions (workspace_id, work_order_id, kind, ref, body, added_by, added_at)
VALUES (:workspace_id, :work_order_id, :kind, :ref, :body, :added_by, :now)
ON CONFLICT (workspace_id, work_order_id, kind, ref) DO NOTHING`)

	// stmtPackSamples reads every generation that started a session of its own with a pack in its
	// system prompt, because a resumed one carries a conversation in its figure.
	stmtPackSamples = define("pack.samples",
		`SELECT length(p.body), g.baseline_size
  FROM generations g
  JOIN context_packs p ON p.workspace_id = g.workspace_id AND p.id = g.pack_id AND p.version = g.pack_version
 WHERE g.workspace_id = :workspace_id AND g.baseline_size > 0 AND g.start_reason IN ('initial', 'handoff')
 ORDER BY g.lane_id, g.generation`)
)

func (t packTx) params(extra params) params {
	extra["workspace_id"] = string(t.scope)
	return extra
}

func scanPack(row scanner) (contextpacks.Pack, error) {
	var p contextpacks.Pack
	err := row.Scan(&p.ID, &p.Version, &p.WorkOrder, &p.Digest, &p.TokensEstimate, &p.Text)
	return p, err
}

func (t packTx) items(pack contextpacks.Pack) (contextpacks.Pack, error) {
	rows, err := t.rows(stmtPackItems, t.params(params{"pack_id": pack.ID, "version": pack.Version}))
	if err != nil {
		return contextpacks.Pack{}, err
	}
	defer rows.Close()
	pack.Items = []contextpacks.Item{}
	for rows.Next() {
		var item contextpacks.Item
		var indexed int64
		if err := rows.Scan(&item.Kind, &item.Ref, &item.Digest, &item.Trust, &indexed); err != nil {
			return contextpacks.Pack{}, err
		}
		item.Indexed = indexed != 0
		pack.Items = append(pack.Items, item)
	}
	return pack, rows.Err()
}

func (t packTx) Pack(id string, version int64) (contextpacks.Pack, error) {
	row, err := t.row(stmtReadPack, t.params(params{"id": id, "version": version}))
	if err != nil {
		return contextpacks.Pack{}, err
	}
	return t.found(row, id)
}

func (t packTx) Latest(id string) (contextpacks.Pack, error) {
	row, err := t.row(stmtLatestPack, t.params(params{"id": id}))
	if err != nil {
		return contextpacks.Pack{}, err
	}
	return t.found(row, id)
}

func (t packTx) found(row *sql.Row, id string) (contextpacks.Pack, error) {
	pack, err := scanPack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return contextpacks.Pack{}, fmt.Errorf("pack %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return contextpacks.Pack{}, err
	}
	return t.items(pack)
}

func (t packTx) All() ([]contextpacks.Pack, error) {
	rows, err := t.rows(stmtAllPacks, t.params(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var packs []contextpacks.Pack
	for rows.Next() {
		pack, err := scanPack(rows)
		if err != nil {
			return nil, err
		}
		packs = append(packs, pack)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, pack := range packs {
		if packs[i], err = t.items(pack); err != nil {
			return nil, err
		}
	}
	return packs, nil
}

func (t packTx) names(st *statement, p params) ([]string, error) {
	rows, err := t.rows(st, t.params(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (t packTx) Pinning(kind contextpacks.Kind, record string) ([]string, error) {
	return t.names(stmtPinning, params{"kind": string(kind), "record_id": record})
}

func (t packTx) Lanes(pack string) ([]string, error) {
	return t.names(stmtPackLanes, params{"pack_id": pack})
}

func scanAddition(row scanner) (contextpacks.Addition, error) {
	var a contextpacks.Addition
	err := row.Scan(&a.WorkOrder, &a.Kind, &a.Ref, &a.Text, &a.AddedBy, &a.AddedAt)
	return a, err
}

func (t packTx) additions(st *statement, p params) ([]contextpacks.Addition, error) {
	rows, err := t.rows(st, t.params(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contextpacks.Addition
	for rows.Next() {
		addition, err := scanAddition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, addition)
	}
	return out, rows.Err()
}

func (t packTx) Additions(workOrder string) ([]contextpacks.Addition, error) {
	return t.additions(stmtAdditions, params{"work_order_id": workOrder})
}

func (t packTx) AllAdditions() ([]contextpacks.Addition, error) {
	return t.additions(stmtAllAdditions, params{})
}

func (t packTx) Samples() ([]contextpacks.Sample, error) {
	rows, err := t.rows(stmtPackSamples, t.params(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contextpacks.Sample
	for rows.Next() {
		var sample contextpacks.Sample
		if err := rows.Scan(&sample.Characters, &sample.BaselineTokens); err != nil {
			return nil, err
		}
		out = append(out, sample)
	}
	return out, rows.Err()
}

func (t packTx) Insert(pack contextpacks.Pack) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertPack, t.params(params{
		"id": pack.ID, "version": pack.Version, "work_order_id": pack.WorkOrder, "digest": pack.Digest,
		"tokens_estimate": pack.TokensEstimate, "body": pack.Text, "now": now,
	}))
	if err != nil {
		return err
	}
	for position, item := range pack.Items {
		indexed := int64(0)
		if item.Indexed {
			indexed = 1
		}
		_, err := t.exec(stmtInsertPackItem, t.params(params{
			"pack_id": pack.ID, "version": pack.Version, "position": int64(position), "kind": string(item.Kind), "ref": item.Ref,
			"record_id": item.Record(), "digest": item.Digest, "trust": string(item.Trust), "indexed": indexed,
		}))
		if err != nil {
			return err
		}
	}
	return nil
}

func (t packTx) Add(addition contextpacks.Addition) (bool, error) {
	added, err := t.exec(stmtAddItem, t.params(params{
		"work_order_id": addition.WorkOrder, "kind": string(addition.Kind), "ref": addition.Ref, "body": addition.Text,
		"added_by": addition.AddedBy, "now": addition.AddedAt,
	}))
	return added != 0, err
}

func (t *readTx) Packs() ports.PackReader { return packTx{session: t.session, scope: t.scope} }

func (t *writeTx) Packs() ports.PackWriter { return packTx{session: t.session, scope: t.scope} }
