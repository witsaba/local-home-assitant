package devices

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// querierRow is a test double for pgx.Row that returns real values on Scan.
type querierRow struct {
	mac, name, fw, chip, ip string
}

func (r *querierRow) Scan(dest ...any) error {
	if len(dest) != 5 {
		return errors.New("querierRow.Scan: expected 5 arguments")
	}
	if s, ok := dest[0].(*string); ok {
		*s = r.mac
	}
	if s, ok := dest[1].(*string); ok {
		*s = r.name
	}
	if s, ok := dest[2].(*string); ok {
		*s = r.fw
	}
	if s, ok := dest[3].(*string); ok {
		*s = r.chip
	}
	if s, ok := dest[4].(*string); ok {
		*s = r.ip
	}
	return nil
}

// querierRow6 is a test double for the ListActive 6-column scan.
type querierRow6 struct {
	mac, name, fw, chip, ip string
	lastSeen                time.Time
}

func (r *querierRow6) scan6(dest ...any) error {
	if len(dest) != 6 {
		return errors.New("querierRow6.scan6: expected 6 arguments")
	}
	if s, ok := dest[0].(*string); ok {
		*s = r.mac
	}
	if s, ok := dest[1].(*string); ok {
		*s = r.name
	}
	if s, ok := dest[2].(*string); ok {
		*s = r.fw
	}
	if s, ok := dest[3].(*string); ok {
		*s = r.chip
	}
	if p, ok := dest[4].(**string); ok {
		if r.ip != "" {
			s := r.ip
			*p = &s
		} else {
			*p = nil
		}
	}
	if t, ok := dest[5].(*time.Time); ok {
		*t = r.lastSeen
	}
	return nil
}

// fakeRows is a minimal fake pgx.Rows for ListActive tests.
type fakeRows struct {
	rows    []querierRow6
	pos     int
	closed  bool
	scanErr error
}

func (f *fakeRows) Next() bool {
	if f.closed || f.pos >= len(f.rows) {
		return false
	}
	f.pos++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	if f.scanErr != nil {
		return f.scanErr
	}
	return f.rows[f.pos-1].scan6(dest...)
}

func (f *fakeRows) Err() error { return f.scanErr }
func (f *fakeRows) Close()     { f.closed = true }

func (f *fakeRows) CommandTag() pgconn.CommandTag      { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRows) Values() ([]any, error)              { return nil, nil }
func (f *fakeRows) RawValues() [][]byte                  { return nil }
func (f *fakeRows) Conn() *pgx.Conn                      { return nil }
func (f *fakeRows) TypeMap() *pgtype.Map                { return nil }

// fakeQuerier is a fake implementation of Querier for unit testing.
type fakeQuerier struct {
	rows   []querierRow
	noRows bool

	// For ListActive: pre-built fakeRows
	listActiveRows *fakeRows

	// For ListAll: pre-built fakeRows
	listAllRows *fakeRows

	// lastSQL records the SQL of the most recent Query/QueryRow call so
	// tests can assert on clauses the fake does not otherwise enforce
	// (e.g. the ORDER BY that the interface promises).
	lastSQL string
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.lastSQL = sql
	if f.noRows {
		return &pgxNoRowsRow{}
	}
	if len(f.rows) == 0 {
		return &pgxNoRowsRow{}
	}
	r := f.rows[0]
	f.rows = f.rows[1:]
	return &r
}

func (f *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	f.lastSQL = sql
	switch {
	case f.listAllRows != nil:
		f.listAllRows.pos = 0
		f.listAllRows.closed = false
		return f.listAllRows, nil
	case f.listActiveRows != nil:
		f.listActiveRows.pos = 0
		f.listActiveRows.closed = false
		return f.listActiveRows, nil
	}
	return &fakeRows{}, nil
}

// pgxNoRowsRow is a pgx.Row that always returns pgx.ErrNoRows on Scan.
type pgxNoRowsRow struct{}

func (r *pgxNoRowsRow) Scan(_ ...any) error {
	return pgx.ErrNoRows
}

func TestPgxGetByMAC_Success(t *testing.T) {
	q := &fakeQuerier{rows: []querierRow{
		{mac: "e08cfe3091b0", name: "kitchen-cam", fw: "0.1.0", chip: "esp32cam", ip: "192.168.1.100"},
	}}
	repo := NewPgx(q)

	dev, err := repo.GetByMAC(context.Background(), "e08cfe3091b0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dev.MAC != "e08cfe3091b0" {
		t.Errorf("MAC: got %q, want %q", dev.MAC, "e08cfe3091b0")
	}
	if dev.Name != "kitchen-cam" {
		t.Errorf("Name: got %q, want %q", dev.Name, "kitchen-cam")
	}
	if dev.FW != "0.1.0" {
		t.Errorf("FW: got %q, want %q", dev.FW, "0.1.0")
	}
	if dev.Chip != "esp32cam" {
		t.Errorf("Chip: got %q, want %q", dev.Chip, "esp32cam")
	}
	if !dev.LastSourceIP.Equal(net.ParseIP("192.168.1.100")) {
		t.Errorf("LastSourceIP: got %v, want %v", dev.LastSourceIP, net.ParseIP("192.168.1.100"))
	}
}

func TestPgxGetByMAC_NotFound(t *testing.T) {
	q := &fakeQuerier{noRows: true}
	repo := NewPgx(q)

	_, err := repo.GetByMAC(context.Background(), "notreal")
	if !errors.Is(err, NotFoundError) {
		t.Errorf("error: got %v, want NotFoundError", err)
	}
}

func TestPgxGetByMAC_EmptyMAC(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q)

	_, err := repo.GetByMAC(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty MAC, got nil")
	}
}

func TestPgxGetByMAC_OptionalFieldsEmpty(t *testing.T) {
	q := &fakeQuerier{rows: []querierRow{
		{mac: "abc123", name: "", fw: "", chip: "", ip: "10.0.0.5"},
	}}
	repo := NewPgx(q)

	dev, err := repo.GetByMAC(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dev.Name != "" {
		t.Errorf("Name: got %q, want empty", dev.Name)
	}
	if dev.FW != "" {
		t.Errorf("FW: got %q, want empty", dev.FW)
	}
	if dev.Chip != "" {
		t.Errorf("Chip: got %q, want empty", dev.Chip)
	}
	if !dev.LastSourceIP.Equal(net.ParseIP("10.0.0.5")) {
		t.Errorf("LastSourceIP: got %v, want %v", dev.LastSourceIP, net.ParseIP("10.0.0.5"))
	}
}

func TestPgxGetByMAC_NullIP(t *testing.T) {
	q := &fakeQuerier{rows: []querierRow{
		{mac: "e08cfe3091b0", name: "", fw: "", chip: "", ip: ""},
	}}
	repo := NewPgx(q)

	dev, err := repo.GetByMAC(context.Background(), "e08cfe3091b0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dev.LastSourceIP != nil {
		t.Errorf("LastSourceIP: got %v, want nil (no IP known)", dev.LastSourceIP)
	}
}

func TestPgxListActive_Success(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	q := &fakeQuerier{
		listActiveRows: &fakeRows{
			rows: []querierRow6{
				{mac: "e08cfe3091b0", name: "kitchen-cam", fw: "0.1.0", chip: "esp32cam", ip: "192.168.1.100", lastSeen: now},
				{mac: "d4e9f48d381c", name: "garage-cam", fw: "0.2.0", chip: "esp32s3", ip: "192.168.1.101", lastSeen: now.Add(-30 * time.Second)},
			},
		},
	}
	repo := NewPgx(q)

	devs, err := repo.ListActive(context.Background(), 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("len(devs): got %d, want 2", len(devs))
	}
	if devs[0].MAC != "e08cfe3091b0" {
		t.Errorf("devs[0].MAC: got %q, want %q", devs[0].MAC, "e08cfe3091b0")
	}
	if devs[1].MAC != "d4e9f48d381c" {
		t.Errorf("devs[1].MAC: got %q, want %q", devs[1].MAC, "d4e9f48d381c")
	}
	if !devs[0].LastSourceIP.Equal(net.ParseIP("192.168.1.100")) {
		t.Errorf("devs[0].LastSourceIP: got %v, want %v", devs[0].LastSourceIP, net.ParseIP("192.168.1.100"))
	}
}

func TestPgxListActive_Empty(t *testing.T) {
	q := &fakeQuerier{
		listActiveRows: &fakeRows{rows: nil},
	}
	repo := NewPgx(q)

	devs, err := repo.ListActive(context.Background(), 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("len(devs): got %d, want 0 (empty result)", len(devs))
	}
}

func TestPgxListActive_NullIP(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	q := &fakeQuerier{
		listActiveRows: &fakeRows{
			rows: []querierRow6{
				{mac: "abc123", name: "no-ip-cam", fw: "0.1.0", chip: "esp32", ip: "", lastSeen: now},
			},
		},
	}
	repo := NewPgx(q)

	devs, err := repo.ListActive(context.Background(), 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("len(devs): got %d, want 1", len(devs))
	}
	if devs[0].LastSourceIP != nil {
		t.Errorf("devs[0].LastSourceIP: got %v, want nil", devs[0].LastSourceIP)
	}
}

func TestPgxListActive_ZeroMaxAge(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q)

	_, err := repo.ListActive(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error for zero maxAge, got nil")
	}
}

func TestPgxListActive_NegativeMaxAge(t *testing.T) {
	q := &fakeQuerier{}
	repo := NewPgx(q)

	_, err := repo.ListActive(context.Background(), -30*time.Second)
	if err == nil {
		t.Fatal("expected error for negative maxAge, got nil")
	}
}

func TestPgxListAll_Success(t *testing.T) {
	// last_seen_at deliberately far in the past: ListAll must ignore
	// freshness entirely, which is the entire point of the method.
	old := time.Now().Add(-30 * 24 * time.Hour)
	q := &fakeQuerier{
		listAllRows: &fakeRows{
			rows: []querierRow6{
				{mac: "d4e9f48d381c", name: "garage-cam", fw: "0.2.0", chip: "esp32s3", ip: "192.168.1.101", lastSeen: old},
				{mac: "e08cfe3091b0", name: "kitchen-cam", fw: "0.1.0", chip: "esp32cam", ip: "192.168.1.100", lastSeen: old},
			},
		},
	}
	repo := NewPgx(q)

	devs, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("len(devs): got %d, want 2", len(devs))
	}
	if devs[0].MAC != "d4e9f48d381c" || devs[1].MAC != "e08cfe3091b0" {
		t.Errorf("MACs: got [%s %s], want [d4e9f48d381c e08cfe3091b0]", devs[0].MAC, devs[1].MAC)
	}
	if devs[0].Name != "garage-cam" {
		t.Errorf("devs[0].Name: got %q, want %q", devs[0].Name, "garage-cam")
	}
	if !devs[0].LastSourceIP.Equal(net.ParseIP("192.168.1.101")) {
		t.Errorf("devs[0].LastSourceIP: got %v, want %v", devs[0].LastSourceIP, net.ParseIP("192.168.1.101"))
	}
}

// TestPgxListAll_OrdersByMAC pins the sort order the interface promises.
// The fake cannot enforce ORDER BY, so the clause is asserted on the SQL.
func TestPgxListAll_OrdersByMAC(t *testing.T) {
	q := &fakeQuerier{listAllRows: &fakeRows{}}
	repo := NewPgx(q)

	if _, err := repo.ListAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.lastSQL, "ORDER BY mac") {
		t.Errorf("ListAll SQL must order by mac for a stable camera list, got: %s", q.lastSQL)
	}
}

func TestPgxListAll_Empty(t *testing.T) {
	q := &fakeQuerier{listAllRows: &fakeRows{rows: nil}}
	repo := NewPgx(q)

	devs, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("len(devs): got %d, want 0 (empty result)", len(devs))
	}
}

func TestPgxListAll_NullIP(t *testing.T) {
	q := &fakeQuerier{
		listAllRows: &fakeRows{
			rows: []querierRow6{
				{mac: "abc123", name: "no-ip-cam", fw: "0.1.0", chip: "esp32", ip: "", lastSeen: time.Now()},
			},
		},
	}
	repo := NewPgx(q)

	devs, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("len(devs): got %d, want 1", len(devs))
	}
	if devs[0].LastSourceIP != nil {
		t.Errorf("devs[0].LastSourceIP: got %v, want nil", devs[0].LastSourceIP)
	}
	if devs[0].Name != "no-ip-cam" {
		t.Errorf("devs[0].Name: got %q, want %q (a known device with no IP keeps its name)", devs[0].Name, "no-ip-cam")
	}
}
