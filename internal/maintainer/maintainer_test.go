package maintainer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jirevwe/gopartman/internal/provisioner"
	"github.com/jirevwe/gopartman/internal/registry"
	"github.com/jirevwe/gopartman/internal/retention"
)

// -----------------------------------------------------------------------------
// Fakes
// -----------------------------------------------------------------------------

type fakeRegistry struct {
	parents []registry.ParentInfo
	tenants map[string][]registry.TenantInfo

	listParentsErr error
	listTenantsErr error

	listParentsCalls atomic.Int32
	listTenantsCalls atomic.Int32
}

func (f *fakeRegistry) ListParents(_ context.Context) ([]registry.ParentInfo, error) {
	f.listParentsCalls.Add(1)
	if f.listParentsErr != nil {
		return nil, f.listParentsErr
	}
	return f.parents, nil
}

func (f *fakeRegistry) ListTenants(_ context.Context, ref registry.ParentRef) ([]registry.TenantInfo, error) {
	f.listTenantsCalls.Add(1)
	if f.listTenantsErr != nil {
		return nil, f.listTenantsErr
	}
	return f.tenants[ref.SchemaName+"."+ref.TableName], nil
}

type ensureCall struct {
	Parent provisioner.ParentRef
	Tenant *provisioner.TenantRef
}

type fakeProvisioner struct {
	mu    sync.Mutex
	calls []ensureCall
	err   error
	// panicOn holds "schema.table" values that must trigger a panic.
	panicOn map[string]bool
}

func (f *fakeProvisioner) EnsurePartitions(_ context.Context, parent provisioner.ParentRef, tenant *provisioner.TenantRef) (provisioner.EnsureReport, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ensureCall{Parent: parent, Tenant: tenant})
	f.mu.Unlock()
	if f.panicOn[parent.SchemaName+"."+parent.TableName] {
		panic("test panic")
	}
	if f.err != nil {
		return provisioner.EnsureReport{}, f.err
	}
	return provisioner.EnsureReport{BoundedCreated: 1, DefaultCreated: true}, nil
}

func (f *fakeProvisioner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type sweepCall struct {
	Parent retention.ParentRef
}

type fakeRetention struct {
	mu    sync.Mutex
	calls []sweepCall
	err   error
}

func (f *fakeRetention) Sweep(_ context.Context, parent retention.ParentRef, _ ...retention.SweepOption) (retention.SweepReport, error) {
	f.mu.Lock()
	f.calls = append(f.calls, sweepCall{Parent: parent})
	f.mu.Unlock()
	if f.err != nil {
		return retention.SweepReport{}, f.err
	}
	return retention.SweepReport{Considered: 0}, nil
}

func (f *fakeRetention) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeLocker struct {
	// held maps "schema.table" to whether another session holds it.
	held map[string]bool
	err  error
	// releaseCalls counts release-func invocations.
	releaseCalls atomic.Int32
}

func (f *fakeLocker) TryLock(_ context.Context, schema, table string) (bool, func(), error) {
	if f.err != nil {
		return false, nil, f.err
	}
	if f.held[schema+"."+table] {
		return false, nil, nil
	}
	return true, func() { f.releaseCalls.Add(1) }, nil
}

type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// discardLogger silences logs during unit tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestImpl builds an Impl wired with the given fakes and a fake
// locker that always grants the lock.
func newTestImpl(t *testing.T, reg ParentLister, prov PartitionEnsurer, ret RetentionSweeper, lk *fakeLocker) *Impl {
	t.Helper()
	if lk == nil {
		lk = &fakeLocker{}
	}
	clk := &fixedClock{t: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)}
	m, err := New(Config{
		Registry:    reg,
		Provisioner: prov,
		Retention:   ret,
		Clock:       clk,
		Logger:      discardLogger(),
		Locker:      lk,
		Schedule:    50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// -----------------------------------------------------------------------------
// New — validation
// -----------------------------------------------------------------------------

func TestNew_RejectsMissingRequired(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"nil Registry", func(c *Config) { c.Registry = nil }, "Registry"},
		{"nil Provisioner", func(c *Config) { c.Provisioner = nil }, "Provisioner"},
		{"nil Retention", func(c *Config) { c.Retention = nil }, "Retention"},
		{"nil Clock", func(c *Config) { c.Clock = nil }, "Clock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				Registry:    &fakeRegistry{},
				Provisioner: &fakeProvisioner{},
				Retention:   &fakeRetention{},
				Clock:       &fixedClock{},
				Logger:      discardLogger(),
				Locker:      &fakeLocker{},
			}
			tc.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("New should reject config missing %s", tc.wantSub)
			}
		})
	}
}

func TestNew_RequiresPoolWhenNoLocker(t *testing.T) {
	cfg := Config{
		Registry:    &fakeRegistry{},
		Provisioner: &fakeProvisioner{},
		Retention:   &fakeRetention{},
		Clock:       &fixedClock{},
		Logger:      discardLogger(),
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("New should reject config missing Pool AND Locker")
	}
}

func TestNew_DefaultsScheduleTo1Hour(t *testing.T) {
	m, err := New(Config{
		Registry:    &fakeRegistry{},
		Provisioner: &fakeProvisioner{},
		Retention:   &fakeRetention{},
		Clock:       &fixedClock{},
		Logger:      discardLogger(),
		Locker:      &fakeLocker{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.schedule != time.Hour {
		t.Errorf("schedule = %s, want 1h", m.schedule)
	}
}

// -----------------------------------------------------------------------------
// Maintain — parent selection
// -----------------------------------------------------------------------------

func TestMaintain_SkipsParentsWithAutomaticMaintenanceFalse(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "off", AutomaticMaintenance: false},
			{SchemaName: "app", TableName: "on", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if prov.callCount() != 1 {
		t.Errorf("provisioner called %d times, want 1", prov.callCount())
	}
	if ret.callCount() != 1 {
		t.Errorf("retention called %d times, want 1", ret.callCount())
	}
	if got := prov.calls[0].Parent.TableName; got != "on" {
		t.Errorf("provisioner ran for %q, want %q", got, "on")
	}
}

func TestMaintain_NoTenantColumn_CallsProvisionerWithNilTenant(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "events", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if got := prov.callCount(); got != 1 {
		t.Fatalf("provisioner calls = %d, want 1", got)
	}
	if prov.calls[0].Tenant != nil {
		t.Errorf("tenant = %+v, want nil", prov.calls[0].Tenant)
	}
	if reg.listTenantsCalls.Load() != 0 {
		t.Errorf("ListTenants was called %d times; expected 0 for non-tenanted parent", reg.listTenantsCalls.Load())
	}
}

func TestMaintain_TenantColumn_CallsProvisionerPerTenant(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "events", TenantColumn: "tenant_id", AutomaticMaintenance: true},
		},
		tenants: map[string][]registry.TenantInfo{
			"app.events": {
				{ParentSchema: "app", ParentName: "events", TenantId: "T1"},
				{ParentSchema: "app", ParentName: "events", TenantId: "T2"},
			},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provisioner calls = %d, want 2", got)
	}
	seen := map[string]bool{}
	for _, c := range prov.calls {
		if c.Tenant == nil {
			t.Error("expected tenant ref, got nil")
			continue
		}
		seen[c.Tenant.TenantId] = true
	}
	if !seen["T1"] || !seen["T2"] {
		t.Errorf("missing tenant calls; seen = %v", seen)
	}
	if ret.callCount() != 1 {
		t.Errorf("retention calls = %d, want 1 (once per parent)", ret.callCount())
	}
}

// -----------------------------------------------------------------------------
// Maintain — advisory-lock skip
// -----------------------------------------------------------------------------

func TestMaintain_LockedParentIsSkipped(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "held", AutomaticMaintenance: true},
			{SchemaName: "app", TableName: "free", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	lk := &fakeLocker{held: map[string]bool{"app.held": true}}
	m := newTestImpl(t, reg, prov, ret, lk)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if prov.callCount() != 1 {
		t.Fatalf("provisioner calls = %d, want 1", prov.callCount())
	}
	if got := prov.calls[0].Parent.TableName; got != "free" {
		t.Errorf("provisioner ran for %q, want %q", got, "free")
	}
	if lk.releaseCalls.Load() != 1 {
		t.Errorf("release calls = %d, want 1 (only the free parent released)", lk.releaseCalls.Load())
	}
}

func TestMaintain_LockErrorIsSkipped(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "events", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	lk := &fakeLocker{err: errors.New("boom")}
	m := newTestImpl(t, reg, prov, ret, lk)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain should not propagate per-parent lock error: %v", err)
	}
	if prov.callCount() != 0 {
		t.Errorf("provisioner should not run when lock errors; got %d calls", prov.callCount())
	}
}

// -----------------------------------------------------------------------------
// Maintain — fault isolation
// -----------------------------------------------------------------------------

func TestMaintain_PanicInOneParent_ContinuesLoop(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "bad", AutomaticMaintenance: true},
			{SchemaName: "app", TableName: "good", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{
		panicOn: map[string]bool{"app.bad": true},
	}
	ret := &fakeRetention{}
	lk := &fakeLocker{}
	m := newTestImpl(t, reg, prov, ret, lk)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain must not propagate panics: %v", err)
	}
	// The bad parent's panic aborts its step, so the good parent's
	// call is the second call to the provisioner.
	if prov.callCount() != 2 {
		t.Fatalf("provisioner calls = %d, want 2", prov.callCount())
	}
	if ret.callCount() != 1 {
		t.Errorf("retention calls = %d, want 1 (only good parent survives)", ret.callCount())
	}
	// The release closure runs from defer even on panic, so both
	// locks were released.
	if lk.releaseCalls.Load() != 2 {
		t.Errorf("release calls = %d, want 2", lk.releaseCalls.Load())
	}
}

func TestMaintain_ProvisionerErrorLogged_LoopContinues(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "a", AutomaticMaintenance: true},
			{SchemaName: "app", TableName: "b", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{err: errors.New("boom")}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Maintain(context.Background()); err != nil {
		t.Fatalf("Maintain must not propagate provisioner error: %v", err)
	}
	// Provisioner is called for both parents even though each errors.
	if prov.callCount() != 2 {
		t.Errorf("provisioner calls = %d, want 2", prov.callCount())
	}
	// Retention still runs for both parents (the ADR says retention
	// runs after provisioner regardless of provisioner outcome).
	if ret.callCount() != 2 {
		t.Errorf("retention calls = %d, want 2", ret.callCount())
	}
}

func TestMaintain_ListParentsError_Propagates(t *testing.T) {
	reg := &fakeRegistry{listParentsErr: errors.New("boom")}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Maintain(context.Background()); err == nil {
		t.Fatal("Maintain should return the ListParents error")
	}
}

func TestMaintain_ContextCanceledMidLoop_Aborts(t *testing.T) {
	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "a", AutomaticMaintenance: true},
			{SchemaName: "app", TableName: "b", AutomaticMaintenance: true},
		},
	}
	prov := &fakeProvisioner{}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Maintain(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if prov.callCount() != 0 {
		t.Errorf("provisioner should not run when ctx already canceled; got %d", prov.callCount())
	}
}

// -----------------------------------------------------------------------------
// Scheduler lifecycle
// -----------------------------------------------------------------------------

func TestStart_SecondCallReturnsError(t *testing.T) {
	reg := &fakeRegistry{}
	m := newTestImpl(t, reg, &fakeProvisioner{}, &fakeRetention{}, nil)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() {
		_ = m.Stop(context.Background())
	}()

	if err := m.Start(context.Background()); err == nil {
		t.Fatal("second Start must return an error")
	}
}

func TestStop_BeforeStart_ReturnsNil(t *testing.T) {
	m := newTestImpl(t, &fakeRegistry{}, &fakeProvisioner{}, &fakeRetention{}, nil)
	if err := m.Stop(context.Background()); err != nil {
		t.Errorf("Stop before Start should be nil, got %v", err)
	}
}

func TestStop_WaitsForInflightTick(t *testing.T) {
	// A parent's provisioner call blocks until we release a signal.
	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	reg := &fakeRegistry{
		parents: []registry.ParentInfo{
			{SchemaName: "app", TableName: "slow", AutomaticMaintenance: true},
		},
	}
	prov := &blockingProvisioner{
		entered: entered,
		release: release,
	}
	ret := &fakeRetention{}
	m := newTestImpl(t, reg, prov, ret, nil)

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until the tick fires and we are blocked inside provisioner.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Maintain never invoked provisioner")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := m.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop returned %v, want context.DeadlineExceeded", err)
	}

	// Let the provisioner finish so the goroutine can exit.
	close(release)

	// The goroutine should eventually exit; a follow-up Stop returns
	// nil because running is already false.
	if err := m.Stop(context.Background()); err != nil {
		t.Errorf("second Stop after deadline: %v", err)
	}
}

type blockingProvisioner struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (b *blockingProvisioner) EnsurePartitions(_ context.Context, _ provisioner.ParentRef, _ *provisioner.TenantRef) (provisioner.EnsureReport, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return provisioner.EnsureReport{}, nil
}
