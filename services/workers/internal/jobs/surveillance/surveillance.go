// surveillance.go — the periodic-background surveillance Job
// registered with the workers host.
//
// Job lifecycle:
//
//   1. NewJob(...) constructs the Job with real implementations
//      of every dependency.
//   2. main.go appends the Job to []worker.Job{...}; the
//      scheduler invokes Run(ctx, emit) every Interval().
//   3. Run queries the devices repo for cameras seen within
//      Freshness, computes the flash flag from Clock.Now() and
//      the configured Window, calls captureOne per camera, and
//      writes the resulting JPEGs to disk via Storage.
//
// Per-camera failures are logged and skipped; an entire tick that
// fails to find any camera is logged at INFO, not ERROR. The
// scheduler keeps ticking either way. The Job never emits
// DiscoveryEvent — see the package doc.
package surveillance

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"github.com/witsaba/local-home-assitant/services/workers/internal/worker"
)

// Job implements worker.Job for periodic camera capture.
//
// All dependencies are injected via NewJob; tests use the Set*
// methods (or call NewJob directly with fakes) to override them.
// The scheduler invokes Run once per Interval(); Run is single-
// threaded within a tick but the scheduler's per-job WaitGroup
// guarantees non-overlapping invocations.
type Job struct {
	name string

	// interval is the duration between two Run invocations.
	interval time.Duration

	// freshness is the threshold for considering a witsaba.devices
	// row "online" — rows whose last_seen_at is older than
	// (now - freshness) are skipped. Default 5 minutes.
	freshness time.Duration

	// captureTimeout is the per-camera HTTP request timeout.
	// Default 10 seconds.
	captureTimeout time.Duration

	// window is the flash time window. Default 17:45 → 05:45.
	window Window

	// storage is the filesystem layer.
	storage Storage

	// repo is the witsaba.devices read interface. The Job uses
	// ListFresh(ctx, olderThan) — see T6 for that addition.
	repo devices.Repository

	// httpClient is the HTTP client used for /capture calls.
	httpClient *http.Client

	// clock is the time source. Defaults to realTimeClock{}.
	clock Clock

	// logger is the zap logger used for every operational
	// message (per-tick summary, per-camera result).
	logger *zap.Logger
}

// NewJob returns a Job wired with real implementations of every
// dependency. The defaults match the task plan and can be
// overridden via the Set* methods.
//
// Parameters:
//   - interval       : time between Run() calls. The user-facing
//                       env var is SURVEILLANCE_INTERVAL_MINUTES.
//   - captureTimeout : per-camera HTTP request timeout.
//                       SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS.
//   - freshness      : how stale a witsaba.devices row may be
//                       before it is skipped.
//                       SURVEILLANCE_DEVICE_FRESHNESS_MINUTES.
//   - rootDir        : absolute path where JPEGs land.
//                       SURVEILLANCE_ROOT_DIR. The caller is
//                       responsible for creating it on the host;
//                       the Job will surface a permission error
//                       if it does not exist.
//   - repo           : the devices.Repository. Must implement
//                       ListFresh(ctx, olderThan).
//   - logger         : non-nil zap logger.
func NewJob(
	interval, captureTimeout, freshness time.Duration,
	rootDir string,
	repo devices.Repository,
	logger *zap.Logger,
) *Job {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if captureTimeout <= 0 {
		captureTimeout = 10 * time.Second
	}
	if freshness <= 0 {
		freshness = 5 * time.Minute
	}
	return &Job{
		name:           "surveillance",
		interval:       interval,
		freshness:      freshness,
		captureTimeout: captureTimeout,
		window:         NewWindow(),
		storage:        NewStorage(rootDir),
		repo:           repo,
		httpClient:     newHTTPClient(captureTimeout),
		clock:          realTimeClock{},
		logger:         logger,
	}
}

// Name implements worker.Job.
func (j *Job) Name() string { return j.name }

// Interval implements worker.Job.
func (j *Job) Interval() time.Duration { return j.interval }

// SetRepo replaces the devices repository. Test seam.
func (j *Job) SetRepo(r devices.Repository) { j.repo = r }

// SetHTTPClient replaces the HTTP client. Test seam — pass a
// client with a transport backed by httptest.Server.
func (j *Job) SetHTTPClient(c *http.Client) { j.httpClient = c }

// SetClock replaces the time source. Test seam.
func (j *Job) SetClock(c Clock) { j.clock = c }

// SetStorage replaces the filesystem layer. Test seam — pass a
// Storage rooted at t.TempDir().
func (j *Job) SetStorage(s Storage) { j.storage = s }

// SetWindow replaces the flash time window. Test seam.
func (j *Job) SetWindow(w Window) { j.window = w }

// SetLogger replaces the logger. Test seam — pass a zap.NewNop()
// in unit tests that do not assert on log output.
func (j *Job) SetLogger(l *zap.Logger) { j.logger = l }

// Run implements worker.Job. One tick:
//
//   1. Read fresh devices from the repo (rows seen within
//      freshness window). ctx.Err() is checked first so we
//      skip work cleanly when the worker is shutting down.
//   2. If 0 devices: log INFO and return nil. The scheduler
//      keeps ticking.
//   3. Compute the flash flag from Clock.Now() and the
//      configured Window.
//   4. For each device, build a CaptureRequest and call
//      captureOne. Per-camera failures are logged at WARN and
//      do NOT cause Run to return an error.
//   5. Write the JPEG body via Storage.WriteFile. Write
//      failures are logged at WARN.
//   6. Log a per-tick summary at INFO.
//
// The emit callback is unused — the surveillance Job does not
// emit DiscoveryEvents. We accept it to satisfy worker.Job but
// never call it.
func (j *Job) Run(ctx context.Context, _ func(types.DiscoveryEvent)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := j.clock.Now()
	cutoff := now.Add(-j.freshness)
	flash := shouldFlash(now, j.window)

	j.logger.Info("surveillance tick starting",
		zap.Time("now", now),
		zap.Bool("flash", flash),
		zap.Duration("freshness", j.freshness),
		zap.String("root", j.storage.Root),
	)

	if j.repo == nil {
		j.logger.Warn("surveillance: no repo configured; skipping tick")
		return nil
	}

	devices, err := j.repo.ListFresh(ctx, cutoff)
	if err != nil {
		// A repo error is not a tick error — log and return
		// nil so the scheduler keeps running. The next tick
		// may succeed if the DB hiccup is transient.
		j.logger.Warn("surveillance: ListFresh failed",
			zap.Error(err))
		return nil
	}

	if len(devices) == 0 {
		j.logger.Info("surveillance tick complete",
			zap.Int("cameras", 0),
			zap.Bool("flash", flash),
		)
		return nil
	}

	// Per-tick counters — we use a single var because the loop
	// is short and Go closures over loop variables are subtle.
	var (
		ok, failed, wrote int
		flashCount        int
	)

	for _, dev := range devices {
		if ctx.Err() != nil {
			j.logger.Warn("surveillance: ctx cancelled mid-tick",
				zap.Int("cameras_processed", ok+failed),
				zap.Int("cameras_remaining", len(devices)-(ok+failed)),
			)
			return ctx.Err()
		}

		if dev.MAC == "" || dev.SourceIP == "" {
			j.logger.Warn("surveillance: device row missing MAC/SourceIP; skipping",
				zap.String("mac", dev.MAC),
				zap.String("source_ip", dev.SourceIP),
			)
			failed++
			continue
		}

		req := CaptureRequest{
			MAC:      dev.MAC,
			SourceIP: dev.SourceIP,
			Flash:    flash,
			Timeout:  j.captureTimeout,
		}

		res := captureOne(ctx, j.httpClient, req)
		if res.Err != nil {
			j.logger.Warn("surveillance: capture failed",
				zap.String("mac", res.MAC),
				zap.String("source_ip", res.SourceIP),
				zap.Int("status", res.StatusCode),
				zap.Duration("elapsed", res.Elapsed),
				zap.Bool("flash", res.Flash),
				zap.Error(res.Err),
			)
			failed++
			continue
		}

		ok++
		if res.Flash {
			flashCount++
		}

		path := j.storage.PathFor(now, res.MAC)
		written, werr := j.storage.WriteFile(path, res.Body)
		if werr != nil {
			j.logger.Warn("surveillance: write failed",
				zap.String("mac", res.MAC),
				zap.String("path", path),
				zap.Int("bytes", len(res.Body)),
				zap.Error(werr),
			)
			continue
		}
		wrote++

		j.logger.Info("surveillance: captured",
			zap.String("mac", res.MAC),
			zap.String("source_ip", res.SourceIP),
			zap.String("path", written),
			zap.Int("bytes", len(res.Body)),
			zap.Duration("elapsed", res.Elapsed),
			zap.Bool("flash", res.Flash),
		)
	}

	j.logger.Info("surveillance tick complete",
		zap.Int("cameras", len(devices)),
		zap.Int("ok", ok),
		zap.Int("failed", failed),
		zap.Int("written", wrote),
		zap.Int("flash_used", flashCount),
		zap.Bool("flash_window", flash),
	)

	return nil
}

// compile-time interface check.
var _ worker.Job = (*Job)(nil)
