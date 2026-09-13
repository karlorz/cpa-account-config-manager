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
