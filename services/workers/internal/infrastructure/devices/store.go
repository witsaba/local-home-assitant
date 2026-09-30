package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// Querier is the subset of pgxpool.Pool that the repository uses.
// Defining it here lets tests inject a fake without spinning up a
// real database, and keeps the production implementation small.
//
// pgxpool.Pool satisfies this interface implicitly.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// QuerierQuery is the subset needed for ListFresh. It is kept
// separate from Querier to avoid widening the surface for callers
// that only need Upsert/Exec. The two interfaces are satisfied by
// pgxpool.Pool; tests inject whichever combination they need.
type QuerierQuery interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Pgx is the Postgres-backed Repository. It owns no connection state
// of its own; the singleton pool (or any Querier) is supplied at
// construction.
//
// The schema and GRANTs are created out-of-band by the init scripts in
// services/postgres/init/, so this code does not run CREATE statements.
// If the table is missing, the database will return an error and the
// repository will surface it to the caller.
type Pgx struct {
	q  Querier
	qq QuerierQuery
}

// NewPgx returns a Pgx repository backed by q.
//
// The pool lifecycle is managed by the db package singleton.
// Use this constructor in production code with db.Get().
// Tests may pass a fake Querier for isolated unit testing.
func NewPgx(q Querier) *Pgx {
	// In production both fields point at the same pgxpool.Pool
	// (it satisfies both interfaces). Tests that need to
	// override behavior inject fakes through the with-query
	// constructor below.
	if qq, ok := q.(QuerierQuery); ok {
		return &Pgx{q: q, qq: qq}
	}
	return &Pgx{q: q}
}

// NewPgxWithQuery returns a Pgx repository with a separate query
// interface. Only used by tests that need to swap the Query
// implementation without affecting Exec.
func NewPgxWithQuery(q Querier, qq QuerierQuery) *Pgx {
	return &Pgx{q: q, qq: qq}
}

// upsertSQL is the single statement the repository executes. The
// last_source_ip column is cast from text to inet so the driver does
// not need to guess. first_seen_at is set on insert only; on conflict
// the existing value is preserved so the original observation time is
// retained across refreshes.
const upsertSQL = `
INSERT INTO witsaba.devices
    (mac, name, fw, chip, last_source_ip, first_seen_at, last_seen_at)
VALUES
    ($1, $2, $3, $4, $5::inet, $6, $6)
ON CONFLICT (mac) DO UPDATE SET
    name           = EXCLUDED.name,
    fw             = EXCLUDED.fw,
    chip           = EXCLUDED.chip,
    last_source_ip = EXCLUDED.last_source_ip,
    last_seen_at   = EXCLUDED.last_seen_at
`

// Upsert executes the upsert statement. The ev.MAC is the primary key.
// SourceIP is stored verbatim; the database casts it to inet. An
// empty SourceIP is preserved as NULL via the nil-interface trick on
// the argument list.
//
// Returns the underlying driver error unchanged.
func (p *Pgx) Upsert(ctx context.Context, ev types.DiscoveryEvent) error {
	if ev.MAC == "" {
		return errors.New("devices.Pgx.Upsert: empty MAC")
	}

	// SourceIP is optional. We pass nil instead of "" so the column
	// becomes NULL on insert rather than failing the inet cast. Other
	// nullable text columns fall back to empty strings today; the
	// devices schema treats them as nullable.
	var sourceIPArg any
	if ev.SourceIP != "" {
		sourceIPArg = ev.SourceIP
	}

	var nameArg, fwArg, chipArg any
	if ev.Name != "" {
		nameArg = ev.Name
	}
	if ev.FW != "" {
		fwArg = ev.FW
	}
	if ev.Chip != "" {
		chipArg = ev.Chip
	}

	discoveredAt := ev.DiscoveredAt
	if discoveredAt.IsZero() {
		discoveredAt = nowFn()
	}

	if _, err := p.q.Exec(ctx, upsertSQL,
		ev.MAC,
		nameArg,
		fwArg,
		chipArg,
		sourceIPArg,
		discoveredAt,
	); err != nil {
		return err
	}
	return nil
}

// Close is a no-op. The pool lifecycle is managed by the db package.
// This method exists to satisfy the Repository interface.
func (p *Pgx) Close() {
	// No-op: pool singleton is closed via db.Close().
}

// listFreshSQL returns rows from witsaba.devices whose last_seen_at
// is strictly greater than $1. The MAC is the primary key and is
// returned as a normalized 12-char lowercase hex string (matches
// the contract used by /whoami and the discovery job's upsert).
// Ordering by MAC ascending gives the surveillance job a stable
// iteration order across runs, which makes log output reproducible.
const listFreshSQL = `
SELECT mac, name, fw, chip, host(last_source_ip), last_seen_at
FROM witsaba.devices
WHERE last_seen_at > $1
ORDER BY mac ASC
`

// ListFresh returns every device whose last_seen_at is strictly
// greater than the cutoff time. Used by the surveillance job to
// enumerate cameras without re-running the subnet scan.
//
// olderThan is exclusive: a row whose last_seen_at equals
// olderThan is excluded. This avoids surfacing devices that were
// observed exactly at the freshness boundary as "fresh".
//
// The query is the only place we call host() (Postgres inet
// → text) so the result scan lines up with the existing column
// types and no driver-side cast is required.
//
// Returns rows in MAC-ascending order for deterministic
// surveillance output.
func (p *Pgx) ListFresh(ctx context.Context, olderThan time.Time) ([]types.DiscoveryEvent, error) {
	if p.qq == nil {
		return nil, fmt.Errorf("ListFresh: no Query interface configured")
	}
	rows, err := p.qq.Query(ctx, listFreshSQL, olderThan)
	if err != nil {
		return nil, fmt.Errorf("ListFresh: query: %w", err)
	}
	defer rows.Close()

	var out []types.DiscoveryEvent
	for rows.Next() {
		var ev types.DiscoveryEvent
		var name, fw, chip *string
		var sourceIP *string
		if err := rows.Scan(&ev.MAC, &name, &fw, &chip, &sourceIP, &ev.DiscoveredAt); err != nil {
			return nil, fmt.Errorf("ListFresh: scan: %w", err)
		}
		if name != nil {
			ev.Name = *name
		}
		if fw != nil {
			ev.FW = *fw
		}
		if chip != nil {
			ev.Chip = *chip
		}
		if sourceIP != nil {
			ev.SourceIP = *sourceIP
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListFresh: rows: %w", err)
	}
	return out, nil
}

// Queryer extends Querier with the Query method needed for schema validation.
type Queryer interface {
	Querier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CheckSchema queries information_schema and returns an error if the
// witsaba.devices table is missing or malformed.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'witsaba' AND table_name = 'devices'
		ORDER BY ordinal_position
	`)
	if err != nil {
		return fmt.Errorf("query information_schema: %w", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var colName, dataType, nullable string
		if err := rows.Scan(&colName, &dataType, &nullable); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, colName)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	// Verify required columns exist.
	required := []string{"mac", "name", "fw", "chip", "last_source_ip", "first_seen_at", "last_seen_at"}
	for _, req := range required {
		found := false
		for _, col := range cols {
			if strings.EqualFold(col, req) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing required column: %s", req)
		}
	}

	return nil
}
