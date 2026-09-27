package devices

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// fakeQuerier is a minimal Querier for unit tests. It records every
// Exec call and returns the configured error, if any.
type fakeQuerier struct {
	mu     sync.Mutex
	calls  []fakeCall
	err    error // returned to every Exec call
	closed bool
}

type fakeCall struct {
	sql  string
	args []any
}

func (f *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Defensive copy of args so test mutations cannot leak across calls.
	cp := make([]any, len(args))
	copy(cp, args)
	f.calls = append(f.calls, fakeCall{sql: sql, args: cp})
	if f.err != nil {
		return pgconn.CommandTag{}, f.err
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeQuerier) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestPgxUpsertSendsExpectedSQL(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q, nil)

	fixed := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	// Pin nowFn so the test is deterministic.
	origNow := nowFn
	nowFn = func() time.Time { return fixed }
	defer func() { nowFn = origNow }()

	ev := types.DiscoveryEvent{
		DiscoveredAt: fixed,
		SourceIP:     "192.168.1.51",
		MAC:          "aa:bb:cc:dd:ee:ff",
		Name:         "kitchen-cam",
		FW:           "1.2.3",
		Chip:         "esp32-s3",
	}

	if err := repo.Upsert(context.Background(), ev); err != nil {
		t.Fatalf("Upsert returned unexpected error: %v", err)
	}

	calls := q.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 Exec call, got %d", len(calls))
	}
	c := calls[0]

	wantSQLContains := []string{
		"INSERT INTO witsaba.devices",
		"ON CONFLICT (mac) DO UPDATE",
		"EXCLUDED.last_seen_at",
	}
	for _, want := range wantSQLContains {
		if !strings.Contains(c.sql, want) {
			t.Errorf("SQL missing %q\nfull SQL: %s", want, c.sql)
		}
	}

	// Args: mac, name, fw, chip, sourceIP, discoveredAt (six).
	if len(c.args) != 6 {
		t.Fatalf("expected 6 args, got %d (%v)", len(c.args), c.args)
	}
	if got, want := c.args[0], "aa:bb:cc:dd:ee:ff"; got != want {
		t.Errorf("arg[0] (mac): got %v, want %v", got, want)
	}
	if got, want := c.args[1], "kitchen-cam"; got != want {
		t.Errorf("arg[1] (name): got %v, want %v", got, want)
	}
	if got, want := c.args[4], "192.168.1.51"; got != want {
		t.Errorf("arg[4] (source_ip): got %v, want %v", got, want)
	}
	if got, want := c.args[5], fixed; got != want {
		t.Errorf("arg[5] (discovered_at): got %v, want %v", got, want)
	}
}

func TestPgxUpsertRejectsEmptyMAC(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q, nil)

	err := repo.Upsert(context.Background(), types.DiscoveryEvent{SourceIP: "10.0.0.1"})
	if err == nil {
		t.Fatal("expected error for empty MAC, got nil")
	}
	if len(q.Calls()) != 0 {
		t.Fatalf("expected zero Exec calls, got %d", len(q.Calls()))
	}
}

func TestPgxUpsertEmptySourceIPBecomesNil(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q, nil)

	ev := types.DiscoveryEvent{
		DiscoveredAt: time.Now(),
		MAC:          "11:22:33:44:55:66",
		SourceIP:     "",
		Name:         "",
		FW:           "",
		Chip:         "",
	}
	if err := repo.Upsert(context.Background(), ev); err != nil {
		t.Fatalf("Upsert returned unexpected error: %v", err)
	}

	calls := q.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	// SourceIP must be nil (NULL in SQL), not the empty string.
	if calls[0].args[4] != nil {
		t.Errorf("expected arg[4] (source_ip) to be nil, got %v", calls[0].args[4])
	}
	// Same for the other optional fields.
	if calls[0].args[1] != nil {
		t.Errorf("expected arg[1] (name) to be nil, got %v", calls[0].args[1])
	}
}

func TestPgxUpsertZeroDiscoveredAtUsesNow(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q, nil)

	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	origNow := nowFn
	nowFn = func() time.Time { return fixed }
	defer func() { nowFn = origNow }()

	ev := types.DiscoveryEvent{MAC: "aa:bb:cc:dd:ee:ff"}
	if err := repo.Upsert(context.Background(), ev); err != nil {
		t.Fatalf("Upsert returned unexpected error: %v", err)
	}

	calls := q.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if got, want := calls[0].args[5], fixed; got != want {
		t.Errorf("expected arg[5] (discovered_at) to default to nowFn, got %v want %v", got, want)
	}
}

func TestPgxUpsertSurfacesDriverError(t *testing.T) {
	boom := errors.New("connection refused")
	q := &fakeQuerier{err: boom}
	repo := NewPgx(q, nil)

	err := repo.Upsert(context.Background(), types.DiscoveryEvent{MAC: "aa:bb:cc:dd:ee:ff"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected error to wrap %v, got %v", boom, err)
	}
}

type fakeCloser struct {
	closed bool
}

func (f *fakeCloser) Close() {
	f.closed = true
}

func TestPgxCloseClosesPoolWhenProvided(t *testing.T) {
	q := &fakeQuerier{}
	pool := &fakeCloser{}
	repo := NewPgx(q, pool)

	repo.Close()
	if !pool.closed {
		t.Fatal("expected pool.Close to be called")
	}
	// Second call must not panic and must not call Close again.
	repo.Close()
}

func TestPgxCloseIsSafeWithoutPool(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q, nil)
	repo.Close() // must not panic
}
