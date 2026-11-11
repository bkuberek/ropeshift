package entry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/otel"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// The telemetry store's files, inside the state directory. The token is what a lane's agent sends the
// receiver, and it survives a restart, because a lane outlives its worker and keeps its environment.
const (
	telemetryFileName  = "telemetry.db"
	telemetryTokenFile = "telemetry.token"
)

// How often the store is held to its bounds, and the default of how often the ledger is read into
// spans. The emitter flushes at twice the reading's interval.
const (
	retentionEvery     = time.Minute
	defaultSpansEvery  = 5 * time.Second
	flushesPerReadings = 2
)

// telemetryDeps is every way telemetry reaches past this process, so a test can hold all of them.
type telemetryDeps struct {
	Lookup func(string) (string, bool)
	// Dial opens every connection the exporter opens. Nil is the exporter's own.
	Dial otel.Dial
	Now  func() time.Time
	// Every is how often the ledger is read into spans. Zero takes the default.
	Every time.Duration
}

func liveTelemetryDeps(every time.Duration) telemetryDeps {
	return telemetryDeps{Lookup: os.LookupEnv, Now: time.Now, Every: every}
}

func (d telemetryDeps) every() time.Duration {
	if d.Every <= 0 {
		return defaultSpansEvery
	}
	return d.Every
}

// soloTelemetry is the worker's telemetry: its store, its receiver, the emitter of its own spans and
// metrics, and an exporter only when one is configured.
type soloTelemetry struct {
	store    *sqlite.TelemetryStore
	emitter  *otel.Emitter
	exporter *otel.Exporter
	receiver *http.Server
	lane     worker.LaneTelemetry
	settings lanes.TelemetrySettings
	loops    sync.WaitGroup
	cancel   context.CancelFunc
	every    time.Duration
	// The exporter drains after the emitter's last flush, so it runs apart and stops last.
	exported      sync.WaitGroup
	cancelExports context.CancelFunc
}

// soloTelemetrySettings reads the worker's part of the user's layer. A layer that does not parse
// leaves the defaults, and a lane that reads it reports the fault.
func soloTelemetrySettings(paths api.Paths) lanes.TelemetrySettings {
	raw, err := os.ReadFile(filepath.Join(paths.StateDir, profiles.UserFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		raw = nil
	}
	profile, err := profiles.Resolve(string(raw))
	if err != nil {
		profile, _ = profiles.Resolve()
	}
	return profile.Telemetry.WorkerSettings()
}

// telemetryToken reads the receiver's token, or makes one on a first start.
func telemetryToken(paths api.Paths) (string, error) {
	path := filepath.Join(paths.StateDir, telemetryTokenFile)
	raw, err := os.ReadFile(path)
	if err == nil && len(strings.TrimSpace(string(raw))) >= 32 {
		return strings.TrimSpace(string(raw)), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := hex.EncodeToString(secret)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// startTelemetry opens the store and starts the loops. Nothing leaves this machine unless the
// standard OTLP exporter settings name an endpoint (NFR-PRIV-1), and a setting this exporter cannot
// honor stops the daemon, because a person asked for something it would not do.
func startTelemetry(parent context.Context, paths api.Paths, address string, deps telemetryDeps, stdout, stderr io.Writer) (*soloTelemetry, error) {
	// The loops outlive the daemon's signal, so what the last read of the ledger emits still reaches the
	// store: close is what ends them.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	t, err := openTelemetry(ctx, paths, address, deps, stdout, stderr)
	if err != nil {
		cancel()
		return nil, err
	}
	t.cancel = cancel
	return t, nil
}

func openTelemetry(ctx context.Context, paths api.Paths, address string, deps telemetryDeps, stdout, stderr io.Writer) (*soloTelemetry, error) {
	store, err := sqlite.OpenTelemetry(filepath.Join(paths.StateDir, telemetryFileName))
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	report := func(err error) { fmt.Fprintf(stderr, "ropeshiftd: telemetry: %v\n", err) }
	t := &soloTelemetry{store: store, settings: soloTelemetrySettings(paths)}
	config, configured, err := otel.ConfigFromEnv(deps.Lookup)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if configured {
		config.KeepAccount, config.Dial, config.Report = t.settings.ExportAccount, deps.Dial, report
		t.exporter = otel.NewExporter(config)
		fmt.Fprintf(stdout, "ropeshiftd: telemetry export: %s\n", strings.Join(config.Hosts(), ", "))
	}
	var forward otel.Forwarder
	if t.exporter != nil {
		forward = t.exporter
	}
	t.emitter = otel.NewEmitter(otel.EmitterConfig{
		Resource: []ports.Attribute{
			{Key: "service.name", Value: "ropeshift"}, {Key: "service.version", Value: version},
			{Key: lanes.AttrWorkspace, Value: string(soloScope)}, {Key: lanes.AttrWorker, Value: "solo"},
		},
		Store: store, Forward: forward, Now: deps.Now, Report: report,
	})
	if err := t.listen(ctx, paths, address, forward, deps, stdout, report); err != nil {
		_ = store.Close()
		return nil, err
	}
	t.run(ctx, report, deps)
	return t, nil
}

// listen serves the receiver on loopback. A port something else holds costs only telemetry, so the
// daemon goes on, and its lanes are told nothing about where to send.
func (t *soloTelemetry) listen(ctx context.Context, paths api.Paths, address string, forward otel.Forwarder, deps telemetryDeps, stdout io.Writer, report func(error)) error {
	if address == api.WebOff {
		return nil
	}
	listener, err := server.ListenTelemetry(address)
	if errors.Is(err, syscall.EADDRINUSE) {
		report(err)
		return nil
	}
	if err != nil {
		return err
	}
	token, err := telemetryToken(paths)
	if err != nil {
		_ = listener.Close()
		return err
	}
	t.receiver = &http.Server{
		Handler:           otel.NewReceiver(otel.ReceiverConfig{Store: t.store, Forward: forward, Token: token, Now: deps.Now, Report: report}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	bound := listener.Addr().String()
	t.lane = worker.LaneTelemetry{Endpoint: "http://" + bound, Header: otel.HeaderToken + "=" + token, Workspace: string(soloScope)}
	t.loops.Add(1)
	go func() {
		defer t.loops.Done()
		if err := t.receiver.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			report(err)
		}
	}()
	fmt.Fprintf(stdout, "ropeshiftd: telemetry http://%s\n", bound)
	return nil
}

func (t *soloTelemetry) run(ctx context.Context, report func(error), deps telemetryDeps) {
	now := deps.Now
	t.every = deps.every()
	exports, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.cancelExports = cancel
	t.exported.Add(1)
	go func() {
		defer t.exported.Done()
		t.exporter.Run(exports)
	}()
	t.loops.Add(2)
	go func() {
		defer t.loops.Done()
		t.emitter.Run(ctx, flushesPerReadings*t.every)
	}()
	go func() {
		defer t.loops.Done()
		maxAge, _ := time.ParseDuration(t.settings.RetentionAge)
		ticker := time.NewTicker(retentionEvery)
		defer ticker.Stop()
		for {
			if _, err := t.store.Retain(ctx, now().UnixMilli(), maxAge, t.settings.RetentionBytes); err != nil && ctx.Err() == nil {
				report(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// close stops the receiver, waits for the emitter's last flush, then for the exporter to send what that
// flush queued, and closes the store. Correctness needs none of it, because the daemon is crash-only.
func (t *soloTelemetry) close() {
	t.cancel()
	if t.receiver != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = t.receiver.Shutdown(shutdown)
	}
	t.loops.Wait()
	t.cancelExports()
	t.exported.Wait()
	_ = t.store.Close()
}

// measuresEvery is how often the product measures are copied out as metrics. They need no pipeline,
// so this only feeds a backend's charts.
const measuresEvery = 5 * time.Minute

// measuresTick exports each window's measures through the emitter, to the local store and to an
// exporter when one is configured.
func (t *soloTelemetry) measuresTick(projects ports.Projects) control.Tick {
	return control.Tick{
		Name:  "measures",
		Every: measuresEvery,
		Run: eachProject(projects, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			return control.NewMeasures(store, t.emitter).Export(ctx, p.ID)
		}),
	}
}

// lastRead reads what the ledger recorded since the spans tick last ran, as the daemon stops, so a
// lane that ended just before a stop has its span. The emitter writes it on its last flush, which
// close waits for. A crash skips this, and telemetry may lose what a crash cuts off.
func (t *soloTelemetry) lastRead(spans control.Tick, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := spans.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "ropeshiftd: telemetry: %v\n", err)
	}
}

// spansTick follows every active project's ledger into spans and metrics, from where each log stood when
// the daemon started. A project created after the start is followed from its first event.
func (t *soloTelemetry) spansTick(ctx context.Context, projects ports.Projects) (control.Tick, error) {
	var mu sync.Mutex
	following := map[ledger.WorkspaceID]*control.Spans{}
	follow := func(ctx context.Context, p ledger.Project, store ports.Store, atHead bool) (*control.Spans, error) {
		mu.Lock()
		defer mu.Unlock()
		if spans, ok := following[p.ID]; ok {
			return spans, nil
		}
		spans := control.NewSpans(store, t.emitter)
		if atHead {
			if err := spans.StartAtHead(ctx, p.ID); err != nil {
				return nil, err
			}
		}
		following[p.ID] = spans
		return spans, nil
	}
	err := eachProject(projects, func(ctx context.Context, p ledger.Project, store ports.Store) error {
		_, err := follow(ctx, p, store, true)
		return err
	})(ctx)
	if err != nil {
		return control.Tick{}, err
	}
	return control.Tick{
		Name:  "telemetry",
		Every: t.every,
		Run: eachProject(projects, func(ctx context.Context, p ledger.Project, store ports.Store) error {
			spans, err := follow(ctx, p, store, false)
			if err != nil {
				return err
			}
			_, err = spans.Project(ctx, p.ID)
			return err
		}),
	}, nil
}
