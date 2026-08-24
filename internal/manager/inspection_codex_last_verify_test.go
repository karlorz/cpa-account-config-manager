package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cpa-account-config-manager/internal/cpaapi"
)

func TestInspectionLastVerifyUsesOfficialCodexResponsesNotWHAM(t *testing.T) {
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "codex-1", Name: "codex-1.json", Provider: "codex", Type: "codex",
			Source: "file", Path: "/auths/codex-1.json",
		}},
		details: map[string]cpaapi.HostAuthGetResponse{
			"codex-1": {AuthIndex: "codex-1", Name: "codex-1.json", Path: "/auths/codex-1.json",
				JSON: json.RawMessage(`{"type":"codex","access_token":"upstream-secret","account_id":"chatgpt-account-1"}`)},
		},
	}
	var mu sync.Mutex
	var urls []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		_ = json.NewDecoder(request.Body).Decode(&call)
		mu.Lock()
		urls = append(urls, call.URL)
		mu.Unlock()
		_ = json.NewEncoder(writer).Encode(managementAPICallResponse{
			StatusCode: http.StatusOK,
			Header:     map[string][]string{"X-Codex-Primary-Used-Percent": {"17"}, "X-Codex-Primary-Window-Minutes": {"10080"}},
			Body:       managementAPICallBody("data: {\"type\":\"response.completed\"}\n\n"),
		})
	}))
	defer server.Close()

	app := NewApp(host, []byte("index"))
	app.modelTests.doer = server.Client()
	app.Configure([]byte("data_dir: " + t.TempDir() + "\nmanagement_base_url: " + server.URL + "\n"))
	defer app.Close()
	app.inspection.mu.Lock()
	app.inspection.policy.ModelProbeBatchSize = 1
	app.inspection.mu.Unlock()

	response := app.HandleManagement(t.Context(), cpaapi.ManagementRequest{
		Method: http.MethodPost, Path: "/v0/management/plugins/cpa-account-config-manager/inspection/scan",
		Headers: http.Header{"Authorization": []string{"Bearer current-management-secret"}},
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("scan = %d %s", response.StatusCode, response.Body)
	}
	waitInspectionSweep(t, app.inspection, InspectionSweepStatusCompleted)

	mu.Lock()
	got := append([]string(nil), urls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("inspection last-verify URLs = %v, want official /codex/responses only", got)
	}
}

func TestInspectionLastVerifyTimeoutKeepsQuotaSnapshotAndOmitsNativeHTTP(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 51, 0, 0, time.UTC)
	resetAt := time.Date(2026, time.August, 28, 7, 20, 0, 0, time.UTC)
	host := &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "codex-1", Name: "codex-1.json", Provider: "codex", Type: "codex",
			Source: "file", Path: "/auths/codex-1.json",
		}},
		details: map[string]cpaapi.HostAuthGetResponse{
			"codex-1": {AuthIndex: "codex-1", Name: "codex-1.json", Path: "/auths/codex-1.json",
				JSON: json.RawMessage(`{"type":"codex","access_token":"upstream-secret"}`)},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(managementAPICallResponse{StatusCode: http.StatusRequestTimeout, Body: `{"error":"timeout"}`})
	}))
	defer server.Close()

	app := NewApp(host, []byte("index"))
	app.modelTests.doer = server.Client()
	app.usage.now = func() time.Time { return now }
	app.Configure([]byte("data_dir: " + t.TempDir() + "\nmanagement_base_url: " + server.URL + "\n"))
	defer app.Close()
	app.usage.ObserveCredentialUsage("codex-1", &CodexUsageSnapshot{
		SevenDay:   &UsageWindowSnapshot{UsedPercent: 100, ResetAt: &resetAt, WindowMinutes: 10080},
		ObservedAt: now.Add(-time.Hour),
	})
	app.inspection.now = func() time.Time { return now }
	app.inspection.mu.Lock()
	app.inspection.records["codex-1"] = inspectionRecord{
		Result:        InspectionResult{OwnedDisable: true, Disabled: true, Health: InspectionHealthQuotaLimited, ReasonCode: "quota_exhausted"},
		Signal:        inspectionSignal{StatusCode: http.StatusRequestTimeout, ReasonCode: "unconfirmed_upstream_response", LastFailureAt: now.Add(-time.Hour)},
		DisableReason: "quota_exhausted", DisabledAt: now.Add(-2 * time.Hour),
	}
	app.inspection.policy.ModelProbeBatchSize = 1
	app.inspection.mu.Unlock()

	response := app.HandleManagement(t.Context(), cpaapi.ManagementRequest{
		Method: http.MethodPost, Path: "/v0/management/plugins/cpa-account-config-manager/inspection/scan",
		Headers: http.Header{"Authorization": []string{"Bearer current-management-secret"}},
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("scan = %d %s", response.StatusCode, response.Body)
	}
	waitInspectionSweep(t, app.inspection, InspectionSweepStatusCompleted)

	results := app.inspection.ListResults(InspectionResultQuery{Page: 1, PageSize: 50})
	if len(results.Results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	got := results.Results[0]
	if got.CodexUsage == nil || got.CodexUsage.SevenDay == nil || got.CodexUsage.SevenDay.UsedPercent != 100 {
		t.Fatalf("stale quota replaced after last-verify timeout: %#v", got.CodexUsage)
	}
	if got.SignalSource != InspectionSignalNative || got.StatusCode != 0 {
		t.Fatalf("native quota result signal=%s status=%d, want native/0", got.SignalSource, got.StatusCode)
	}
}

func TestUpdateInspectionRecordOmitsHTTPStatusForNativeQuotaDecision(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 51, 0, 0, time.UTC)
	resetAt := now.Add(4 * 24 * time.Hour)
	account := Account{
		ID: "codex-1", Provider: "codex", Editable: true,
		Usage: &AccountUsageSnapshot{Codex: &CodexUsageSnapshot{
			SevenDay: &UsageWindowSnapshot{UsedPercent: 100, ResetAt: &resetAt, WindowMinutes: 10080}, ObservedAt: now,
		}},
	}
	record := inspectionRecord{
		Signal: inspectionSignal{StatusCode: http.StatusRequestTimeout, ReasonCode: "unconfirmed_upstream_response", LastFailureAt: now.Add(-time.Minute)},
	}
	decision := decideInspection(account, record, now)
	if decision.SignalSource != InspectionSignalNative || decision.ReasonCode != "quota_exhausted" {
		t.Fatalf("decision = %#v", decision)
	}
	updateInspectionRecord(&record, account, decision, now)
	if record.Result.StatusCode != 0 {
		t.Fatalf("native quota result status_code = %d, want 0", record.Result.StatusCode)
	}
}
