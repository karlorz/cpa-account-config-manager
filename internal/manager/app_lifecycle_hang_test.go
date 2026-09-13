package manager

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"cpa-account-config-manager/internal/cpaapi"
)

type blockingListAuthHost struct {
	once    sync.Once
	started chan struct{}
}

func (h *blockingListAuthHost) ListAuth(ctx context.Context) ([]cpaapi.HostAuthFileEntry, error) {
	h.once.Do(func() { close(h.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingListAuthHost) GetAuth(context.Context, string) (cpaapi.HostAuthGetResponse, error) {
	return cpaapi.HostAuthGetResponse{}, errors.New("unexpected get")
}

func (*blockingListAuthHost) SaveAuth(context.Context, string, json.RawMessage) (cpaapi.HostAuthSaveResponse, error) {
	return cpaapi.HostAuthSaveResponse{}, errors.New("unexpected save")
}

func TestConfigureHostReconfigureReturnsWhileHostListAuthBlocks(t *testing.T) {
	dataDir := t.TempDir()
	policy := defaultInspectionPolicy()
	policy.AutoEnable = true
	state := persistedInspectionState{
		Version: inspectionStoreVersion,
		Policy:  policy,
		Records: map[string]inspectionRecord{},
	}
	if errSave := saveInspectionState(inspectionStorePath(dataDir), state); errSave != nil {
		t.Fatalf("save cold-start inspection state: %v", errSave)
	}

	host := &blockingListAuthHost{
		started: make(chan struct{}),
	}
	app := NewApp(host, []byte("index"))
	defer app.Close()

	configYAML := []byte("data_dir: " + dataDir)

	// First ConfigureHost initializes components and starts inspection scanLoop
	app.ConfigureHost(configYAML, cpaapi.SchemaVersion)

	// Wait until startup scan has entered ListAuth
	select {
	case <-host.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for host ListAuth to be entered")
	}

	// Second ConfigureHost (reconfigure with same yaml) must return promptly (within 1s)
	// and not block waiting for the inspection scan to release scanMu.
	reconfigureDone := make(chan struct{})
	go func() {
		app.ConfigureHost(configYAML, cpaapi.SchemaVersion)
		close(reconfigureDone)
	}()

	select {
	case <-reconfigureDone:
	case <-time.After(1 * time.Second):
		t.Fatal("ConfigureHost reconfigure blocked on hanging host ListAuth")
	}
}

func TestConfigureHostReconfigureReturnsWithLiveInspectionPolicyWhileListAuthBlocks(t *testing.T) {
	dataDir := t.TempDir()
	policy := defaultInspectionPolicy()
	policy.AutoEnable = true
	state := persistedInspectionState{
		Version: inspectionStoreVersion,
		Policy:  policy,
		Records: map[string]inspectionRecord{},
	}
	if errSave := saveInspectionState(inspectionStorePath(dataDir), state); errSave != nil {
		t.Fatalf("save cold-start inspection state: %v", errSave)
	}

	host := &blockingListAuthHost{
		started: make(chan struct{}),
	}
	app := NewApp(host, []byte("index"))
	defer app.Close()

	// Initial configuration with base data_dir initializes inspection engine and starts scan
	app.ConfigureHost([]byte("data_dir: "+dataDir), cpaapi.SchemaVersion)

	// First ConfigureHost yaml with production-like inspection_policy AND data_dir,
	// e.g. enabled/auto_enable/auto_disable true plus numeric fields from cloud01
	// (scan_interval_minutes 15, failure_threshold 3, recovery_threshold 2, etc.).
	// Include at least one field that differs from defaultInspectionPolicy() so DeepEqual is not trivially true against defaults.
	configYAML := []byte(`data_dir: ` + dataDir + `
inspection_policy:
  enabled: true
  auto_enable: true
  auto_disable: true
  scan_interval_minutes: 15
  failure_threshold: 3
  recovery_threshold: 2
  model_probe_models:
    gemini: gemini-2.5-flash
`)

	// Wait until startup scan has entered ListAuth
	select {
	case <-host.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for host ListAuth to be entered")
	}

	// Reconfigure with the production-like yaml while ListAuth is blocked
	reconfigureDone := make(chan struct{})
	go func() {
		app.ConfigureHost(configYAML, cpaapi.SchemaVersion)
		close(reconfigureDone)
	}()

	select {
	case <-reconfigureDone:
	case <-time.After(1 * time.Second):
		t.Fatal("ConfigureHost reconfigure blocked with live inspection policy on hanging host ListAuth")
	}
}

func TestConfigureHostReconfigureReturnsWhenInspectionPolicyChangesWhileListAuthBlocks(t *testing.T) {
	dataDir := t.TempDir()
	policy := defaultInspectionPolicy()
	policy.Enabled = true
	policy.AutoEnable = true
	policy.ScanIntervalMinutes = 15
	state := persistedInspectionState{
		Version: inspectionStoreVersion,
		Policy:  policy,
		Records: map[string]inspectionRecord{},
	}
	if errSave := saveInspectionState(inspectionStorePath(dataDir), state); errSave != nil {
		t.Fatalf("save cold-start inspection state: %v", errSave)
	}

	host := &blockingListAuthHost{
		started: make(chan struct{}),
	}
	app := NewApp(host, []byte("index"))
	defer app.Close()

	configYAMLA := []byte(`data_dir: ` + dataDir + `
inspection_policy:
  enabled: true
  auto_enable: true
  scan_interval_minutes: 15
`)

	configYAMLB := []byte(`data_dir: ` + dataDir + `
inspection_policy:
  enabled: true
  auto_enable: true
  scan_interval_minutes: 20
`)

	// First ConfigureHost initializes components and starts inspection scanLoop
	app.ConfigureHost(configYAMLA, cpaapi.SchemaVersion)

	// Wait until startup scan has entered ListAuth
	select {
	case <-host.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for host ListAuth to be entered")
	}

	// Second ConfigureHost with changed inspection_policy must return within 1s
	reconfigureDone := make(chan struct{})
	go func() {
		app.ConfigureHost(configYAMLB, cpaapi.SchemaVersion)
		close(reconfigureDone)
	}()

	select {
	case <-reconfigureDone:
	case <-time.After(1 * time.Second):
		t.Fatal("ConfigureHost reconfigure blocked when inspection policy changed on hanging host ListAuth")
	}
}

func TestInspectionStartupScanClearsBusyWhenListAuthBlocks(t *testing.T) {
	origTimeout := inspectionScanListAuthTimeout
	inspectionScanListAuthTimeout = 200 * time.Millisecond
	defer func() {
		inspectionScanListAuthTimeout = origTimeout
	}()

	dataDir := t.TempDir()
	policy := defaultInspectionPolicy()
	policy.AutoEnable = true
	state := persistedInspectionState{
		Version: inspectionStoreVersion,
		Policy:  policy,
		Records: map[string]inspectionRecord{},
	}
	if errSave := saveInspectionState(inspectionStorePath(dataDir), state); errSave != nil {
		t.Fatalf("save cold-start inspection state: %v", errSave)
	}

	host := &blockingListAuthHost{
		started: make(chan struct{}),
	}
	app := NewApp(host, []byte("index"))
	defer app.Close()

	configYAML := []byte("data_dir: " + dataDir)

	// ConfigureHost initializes components and starts inspection scanLoop
	app.ConfigureHost(configYAML, cpaapi.SchemaVersion)

	// Wait until startup scan has entered ListAuth
	select {
	case <-host.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for host ListAuth to be entered")
	}

	// Snapshot() must become !Running && !Pending within ~2s
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := app.inspection.Snapshot()
		if !snapshot.Running && !snapshot.Pending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	snapshot := app.inspection.Snapshot()
	t.Fatalf("inspection snapshot stayed busy: running=%t pending=%t", snapshot.Running, snapshot.Pending)
}

