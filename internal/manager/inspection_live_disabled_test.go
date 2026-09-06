package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"cpa-account-config-manager/internal/cpaapi"
)

func TestReconcileAccountStatesReclassifiesStaleManualDisabledAfterLiveEnable(t *testing.T) {
	now := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "kimi-account",
			Name:      "kimi.json",
			Provider:  "kimi",
			Type:      "kimi",
			Status:    "active",
			Disabled:  false,
			Source:    "file",
			Path:      "/auths/kimi.json",
		}},
	}
	engine := NewInspectionEngine(NewAccountService(host), host, NewMutationCoordinator())
	engine.now = func() time.Time { return now }
	engine.records["kimi-account"] = inspectionRecord{Result: InspectionResult{
		ID: "kimi-account", Name: "kimi.json", Provider: "kimi",
		Health: InspectionHealthDisabled, ReasonCode: "manual_disabled",
		Confidence: InspectionConfidenceHigh, Recommendation: InspectionRecommendationKeep,
		Disabled: true, LastCheckedAt: now.Add(-time.Hour),
	}}

	if err := engine.ReconcileAccountStates(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	listed := engine.ListResults(InspectionResultQuery{Page: 1, PageSize: 50})
	if listed.Total != 1 || len(listed.Results) != 1 {
		t.Fatalf("listed = %#v", listed)
	}
	result := listed.Results[0]
	if result.Disabled {
		t.Fatalf("live-enabled account still marked disabled: %#v", result)
	}
	if result.Health == InspectionHealthDisabled || result.ReasonCode == "manual_disabled" {
		t.Fatalf("stale manual-disable health survived refresh: %#v", result)
	}
}

func TestListInspectionResultsReconcilesStaleManualDisabledWithoutNewScan(t *testing.T) {
	now := time.Date(2026, time.September, 6, 10, 5, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "kimi-account",
			Name:      "kimi.json",
			Provider:  "kimi",
			Type:      "kimi",
			Status:    "active",
			Disabled:  false,
			Source:    "file",
			Path:      "/auths/kimi.json",
		}},
	}
	app := NewApp(host, []byte("index"))
	app.Configure([]byte(fmt.Sprintf("workers: 1\ndata_dir: %q\n", t.TempDir())))
	defer app.Close()
	app.inspection.now = func() time.Time { return now }
	app.inspection.mu.Lock()
	app.inspection.records["kimi-account"] = inspectionRecord{Result: InspectionResult{
		ID: "kimi-account", Name: "kimi.json", Provider: "kimi",
		Health: InspectionHealthDisabled, ReasonCode: "manual_disabled",
		Confidence: InspectionConfidenceHigh, Recommendation: InspectionRecommendationKeep,
		Disabled: true, LastCheckedAt: now.Add(-time.Hour),
	}}
	app.inspection.mu.Unlock()

	response := app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/plugins/cpa-account-config-manager/inspection/results",
		Query:  map[string][]string{"page": {"1"}, "page_size": {"50"}},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", response.StatusCode, response.Body)
	}
	var listed InspectionResultList
	if errDecode := json.Unmarshal(response.Body, &listed); errDecode != nil {
		t.Fatalf("decode refresh: %v", errDecode)
	}
	if listed.Total != 1 || len(listed.Results) != 1 {
		t.Fatalf("refresh listed = %#v", listed)
	}
	result := listed.Results[0]
	if result.Disabled || result.Health == InspectionHealthDisabled || result.ReasonCode == "manual_disabled" {
		t.Fatalf("Refresh returned stale Disabled health: %#v", result)
	}
}

func TestReconcileAccountStatesKeepsQuotaLimitedAfterEnable(t *testing.T) {
	now := time.Date(2026, time.September, 6, 11, 0, 0, 0, time.UTC)
	recoverAfter := now.Add(time.Hour)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex:      "kimi-account",
			Name:           "kimi.json",
			Provider:       "kimi",
			Type:           "kimi",
			Status:         "active",
			Disabled:       false,
			NextRetryAfter: recoverAfter,
			Source:         "file",
			Path:           "/auths/kimi.json",
		}},
	}
	engine := NewInspectionEngine(NewAccountService(host), host, NewMutationCoordinator())
	engine.now = func() time.Time { return now }
	seedStaleManualDisabled(engine, now)

	if err := engine.ReconcileAccountStates(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	result := engine.ListResults(InspectionResultQuery{Page: 1, PageSize: 50}).Results[0]
	if result.Disabled || result.Health != InspectionHealthQuotaLimited || result.ReasonCode != "quota_exhausted" {
		t.Fatalf("quota evidence lost after enable: %#v", result)
	}
}

func TestReconcileAccountStatesKeepsInvalidCredentialsAfterEnable(t *testing.T) {
	now := time.Date(2026, time.September, 6, 11, 5, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex:     "kimi-account",
			Name:          "kimi.json",
			Provider:      "kimi",
			Type:          "kimi",
			Status:        "invalid_grant",
			StatusMessage: "invalid_grant",
			Disabled:      false,
			Source:        "file",
			Path:          "/auths/kimi.json",
		}},
	}
	engine := NewInspectionEngine(NewAccountService(host), host, NewMutationCoordinator())
	engine.now = func() time.Time { return now }
	seedStaleManualDisabled(engine, now)

	if err := engine.ReconcileAccountStates(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	result := engine.ListResults(InspectionResultQuery{Page: 1, PageSize: 50}).Results[0]
	if result.Disabled || result.Health != InspectionHealthInvalidCredentials || result.ReasonCode != "invalid_credentials" {
		t.Fatalf("invalid credentials lost after enable: %#v", result)
	}
}

func TestReconcileAccountStatesKeepsManualDisabledWhileLiveFileDisabled(t *testing.T) {
	now := time.Date(2026, time.September, 6, 11, 10, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "kimi-account",
			Name:      "kimi.json",
			Provider:  "kimi",
			Type:      "kimi",
			Status:    "active",
			Disabled:  true,
			Source:    "file",
			Path:      "/auths/kimi.json",
		}},
	}
	engine := NewInspectionEngine(NewAccountService(host), host, NewMutationCoordinator())
	engine.now = func() time.Time { return now }
	seedStaleManualDisabled(engine, now)

	if err := engine.ReconcileAccountStates(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	result := engine.ListResults(InspectionResultQuery{Page: 1, PageSize: 50}).Results[0]
	if !result.Disabled || result.Health != InspectionHealthDisabled || result.ReasonCode != "manual_disabled" || result.Recommendation != InspectionRecommendationKeep {
		t.Fatalf("still-disabled file lost Keep: %#v", result)
	}
}

func TestInspectionScanDoesNotKeepManualDisabledOnLiveEnabledAccount(t *testing.T) {
	now := time.Date(2026, time.September, 6, 11, 15, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "kimi-account",
			Name:      "kimi.json",
			Provider:  "kimi",
			Type:      "kimi",
			Status:    "active",
			Disabled:  false,
			Source:    "file",
			Path:      "/auths/kimi.json",
		}},
	}
	engine := NewInspectionEngine(NewAccountService(host), host, NewMutationCoordinator())
	engine.now = func() time.Time { return now }
	engine.Configure(Config{DataDir: t.TempDir()})
	defer engine.Shutdown()
	seedStaleManualDisabled(engine, now)
	engine.scan(context.Background())

	result := engine.ListResults(InspectionResultQuery{Page: 1, PageSize: 50}).Results[0]
	if result.Disabled || result.Health == InspectionHealthDisabled || result.ReasonCode == "manual_disabled" {
		t.Fatalf("later scan kept manual_disabled on an enabled file: %#v", result)
	}
}

func TestApplyAccountEnableReconcilesStaleManualDisabled(t *testing.T) {
	now := time.Date(2026, time.September, 6, 11, 20, 0, 0, time.UTC)
	raw := json.RawMessage(`{"type":"kimi","disabled":true}`)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "kimi-account",
			Name:      "kimi.json",
			Provider:  "kimi",
			Type:      "kimi",
			Status:    "active",
			Disabled:  true,
			Source:    "file",
			Path:      "/auths/kimi.json",
		}},
		details: map[string]cpaapi.HostAuthGetResponse{
			"kimi-account": {AuthIndex: "kimi-account", Name: "kimi.json", Path: "/auths/kimi.json", JSON: raw},
		},
	}
	accounts := NewAccountService(host)
	resolved, errResolve := accounts.ResolveTargets(context.Background(), TargetScope{Mode: "selected", IDs: []string{"kimi-account"}})
	if errResolve != nil || len(resolved.Accounts) != 1 {
		t.Fatalf("resolve: %v %#v", errResolve, resolved)
	}
	inspection := NewInspectionEngine(accounts, host, NewMutationCoordinator())
	inspection.now = func() time.Time { return now }
	seedStaleManualDisabled(inspection, now)

	jobs := NewJobEngine(accounts)
	jobs.SetInspection(inspection)
	enabled := false
	applied := jobs.applyAccount(context.Background(), resolved.Accounts[0], BatchOperationPatch, BatchPatch{Disabled: &enabled}, &liveDisabledWriter{host: host})
	if applied.Status != ResultSucceeded {
		t.Fatalf("enable apply = %#v", applied)
	}
	result := inspection.ListResults(InspectionResultQuery{Page: 1, PageSize: 50}).Results[0]
	if result.Disabled || result.Health == InspectionHealthDisabled || result.ReasonCode == "manual_disabled" {
		t.Fatalf("enable write left stale Disabled health: %#v", result)
	}
}

func seedStaleManualDisabled(engine *InspectionEngine, now time.Time) {
	engine.records["kimi-account"] = inspectionRecord{Result: InspectionResult{
		ID: "kimi-account", Name: "kimi.json", Provider: "kimi",
		Health: InspectionHealthDisabled, ReasonCode: "manual_disabled",
		Confidence: InspectionConfidenceHigh, Recommendation: InspectionRecommendationKeep,
		Disabled: true, LastCheckedAt: now.Add(-time.Hour),
	}}
}

type liveDisabledWriter struct {
	host *fakeAuthHost
}

func (w *liveDisabledWriter) PatchFields(context.Context, string, BatchPatch) error {
	return nil
}

func (w *liveDisabledWriter) PatchDisabled(_ context.Context, name string, disabled bool) error {
	w.host.mu.Lock()
	defer w.host.mu.Unlock()
	for index := range w.host.entries {
		if w.host.entries[index].Name == name {
			w.host.entries[index].Disabled = disabled
		}
	}
	return nil
}

func (w *liveDisabledWriter) DeleteAuthFile(context.Context, string) error {
	return nil
}
