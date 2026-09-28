package devices

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier is the minimal interface of pgxpool.Pool that the repository uses.
// Defining it here lets tests inject a fake without spinning up a real database.
// pgxpool.Pool satisfies this interface implicitly.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Pgx is the Postgres-backed DeviceRepository. It owns no connection state
// of its own; the singleton pool (or any Querier) is supplied at
// construction.
//
// The schema and GRANTs are created out-of-band by the init scripts in
// services/postgres/init/, so this code does not run CREATE statements.
// If the table is missing, the database will return an error and the
// repository will surface it to the caller.
type Pgx struct {
	q Querier
}

// NewPgx returns a Pgx repository backed by q.
//
// The pool lifecycle is managed by the db package singleton.
// Use this constructor in production code with db.Get().
// Tests may pass a fake Querier for isolated unit testing.
func NewPgx(q Querier) *Pgx {
	return &Pgx{q: q}
}

// selectSQL fetches a single device by MAC. host(last_source_ip) extracts
// the IP address as text from the INET column.
const selectSQL = `
SELECT
    mac,
    COALESCE(name, ''),
    COALESCE(fw, ''),
    COALESCE(chip, ''),
    host(last_source_ip)
FROM witsaba.devices
WHERE mac = $1
`

// GetByMAC executes the SELECT query and returns a Device.
// Returns NotFoundError if the query returns no rows.
// Returns any database error unchanged.
func (p *Pgx) GetByMAC(ctx context.Context, mac string) (*Device, error) {
	if mac == "" {
		return nil, errors.New("devices.Pgx.GetByMAC: mac is empty")
	}

	var dev Device
	var ipStr string

	err := p.q.QueryRow(ctx, selectSQL, mac).Scan(
		&dev.MAC,
		&dev.Name,
		&dev.FW,
		&dev.Chip,
		&ipStr,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, NotFoundError
		}
		return nil, err
	}

	dev.LastSourceIP = net.ParseIP(ipStr)
	if dev.LastSourceIP == nil {
		// NULL last_source_ip → net.IP is nil (zero value).
		// We treat a missing IP as a valid device with no known address.
	}

	return &dev, nil
}

// listActiveSQL fetches all devices seen within maxAge. host(last_source_ip)
// extracts the IP as text; $1 is formatted as an interval (e.g. "60s").
const listActiveSQL = `
SELECT
    mac,
    COALESCE(name, ''),
    COALESCE(fw, ''),
    COALESCE(chip, ''),
    host(last_source_ip),
    last_seen_at
FROM witsaba.devices
WHERE last_seen_at >= now() - $1::interval
ORDER BY last_seen_at DESC
`

// ListActive returns all devices whose last_seen_at is within maxAge of now.
// maxAge must be positive. Returns an empty slice and nil error when no
// devices match.
func (p *Pgx) ListActive(ctx context.Context, maxAge time.Duration) ([]*Device, error) {
	if maxAge <= 0 {
		return nil, errors.New("devices.Pgx.ListActive: maxAge must be positive")
	}

	rows, err := p.q.Query(ctx, listActiveSQL, maxAge.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devs []*Device
	for rows.Next() {
		var d Device
		var ipStr *string // nullable: last_source_ip can be NULL
		if err := rows.Scan(&d.MAC, &d.Name, &d.FW, &d.Chip, &ipStr, &d.LastSeenAt); err != nil {
			return nil, err
		}
		if ipStr != nil {
			d.LastSourceIP = net.ParseIP(*ipStr)
		}
		devs = append(devs, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return devs, nil
}
