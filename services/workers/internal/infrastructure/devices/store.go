package devices

import (
	"context"
	"errors"
	"io"

	"github.com/jackc/pgx/v5/pgconn"

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

// Pgx is the Postgres-backed Repository. It owns no connection state
// of its own; the pool (or any Querier) is supplied at construction.
//
// The schema and GRANTs are created out-of-band by the init scripts in
// services/postgres/init/, so this code does not run CREATE statements.
// If the table is missing, the database will return an error and the
// repository will surface it to the caller.
type Pgx struct {
	q     Querier
	pool  io.Closer // optional; if non-nil, Close() will close it
}

// NewPgx returns a Pgx repository backed by q.
//
// If pool is non-nil, the returned repository will close it on Close().
// Pass nil for pool when q does not need explicit teardown (e.g. tests
// using a fake Querier). Production code passes *pgxpool.Pool.
func NewPgx(q Querier, pool io.Closer) *Pgx {
	return &Pgx{q: q, pool: pool}
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

// Close closes the underlying pool if one was supplied at construction.
// Safe to call when no pool was supplied or after a previous Close.
func (p *Pgx) Close() {
	if p.pool == nil {
		return
	}
	_ = p.pool.Close()
	p.pool = nil
}
