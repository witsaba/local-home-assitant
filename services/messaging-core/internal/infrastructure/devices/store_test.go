package devices

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
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

// fakeQuerier is a fake implementation of Querier for unit testing.
type fakeQuerier struct {
	rows []querierRow
	noRows bool // when true, QueryRow returns a row that returns pgx.ErrNoRows
}

func (f *fakeQuerier) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
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
