package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cpa-account-config-manager/internal/cpaapi"
)

func TestQuotaMetadataParsersPreserveZeroAndOfficialPrecedence(t *testing.T) {
	plan, usageCount, valid := parseQuotaUsageMetadata([]byte(`{"planType":"Free","rateLimitResetCredits":{"availableCount":"7"}}`))
	if !valid || plan != "free" || usageCount == nil || *usageCount != 7 {
		t.Fatalf("usage metadata = plan:%q count:%v valid:%t", plan, usageCount, valid)
	}
	count, valid := parseQuotaResetCredits([]byte(`{"available_count":0,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-07-28T00:00:00Z"}]}`))
	if !valid || count == nil || *count != 0 {
		t.Fatalf("reset count = %v valid:%t, want known zero", count, valid)
	}
	count, valid = parseQuotaResetCredits([]byte(`{"credits":[{"resetType":"codex_rate_limits","status":"available","expiresAt":"2026-07-28T00:00:00Z"},{"reset_type":"other","status":"available","expires_at":"2026-07-28T00:00:00Z"}]}`))
	if !valid || count == nil || *count != 1 {
		t.Fatalf("credit fallback = %v valid:%t, want one", count, valid)
	}
	for _, raw := range []string{
		`{"available_count":-1}`,
		`{"available_count":1.5}`,
		`{"available_count":1000001}`,
		`{"unexpected":"shape"}`,
	} {
		if parsed, ok := parseQuotaResetCredits([]byte(raw)); ok && parsed != nil {
			t.Fatalf("unsafe count %s parsed as %v", raw, *parsed)
		}
	}
}

func TestQuotaMetadataRefreshUsesCPAAPIAndPersistsPlanFirstType(t *testing.T) {
	dataDir := t.TempDir()
	host := quotaMetadataHost()
	var mu sync.Mutex
	requests := make([]managementAPICallRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v0/management/api-call" {
			t.Errorf("management request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer management-secret" {
			t.Errorf("management authorization was not forwarded")
		}
		var call managementAPICallRequest
		if errDecode := json.NewDecoder(request.Body).Decode(&call); errDecode != nil {
			t.Errorf("decode API call: %v", errDecode)
		}
		mu.Lock()
		requests = append(requests, call)
		mu.Unlock()
		body := `{"available_count":0,"credits":[]}`
		if call.URL == codexQuotaUsageURL {
			body = `{"plan_type":"Free","rate_limit_reset_credits":{"available_count":6},"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600}}}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()

	app := NewApp(host, []byte("index"))
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", dataDir, server.URL)))
	response := app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method:  http.MethodPost,
		Path:    "/v0/management/plugins/cpa-account-config-manager/accounts/quota-metadata/refresh",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}},
		Body:    []byte(`{"account_id":"auth-1"}`),
	})
	if response.StatusCode != http.StatusOK {
		app.Close()
		t.Fatalf("refresh = %d %s", response.StatusCode, response.Body)
	}
	var refreshed QuotaMetadataResponse
	if errDecode := json.Unmarshal(response.Body, &refreshed); errDecode != nil {
		app.Close()
		t.Fatalf("decode response: %v", errDecode)
	}
	if refreshed.PlanType != "free" || refreshed.ActiveResetCount == nil || *refreshed.ActiveResetCount != 0 {
		app.Close()
		t.Fatalf("refresh result = %#v", refreshed)
	}
	listed, errList := app.accounts.List(context.Background(), ListQuery{Page: 1, PageSize: 50})
	if errList != nil || len(listed.Accounts) != 1 || listed.Accounts[0].PlanType != "free" || listed.Accounts[0].Usage == nil || listed.Accounts[0].Usage.Codex == nil || listed.Accounts[0].Usage.Codex.ActiveResetCount == nil || *listed.Accounts[0].Usage.Codex.ActiveResetCount != 0 {
		app.Close()
		t.Fatalf("listed account = %#v err=%v", listed.Accounts, errList)
	}
	app.Close()

	mu.Lock()
	captured := append([]managementAPICallRequest(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 || captured[0].URL != codexQuotaUsageURL || captured[1].URL != codexQuotaResetCreditsURL {
		t.Fatalf("API calls = %#v", captured)
	}
	for _, call := range captured {
		if call.AuthIndex != "auth-1" || call.Header["Authorization"] != "Bearer $TOKEN$" || call.Header["Chatgpt-Account-Id"] != "chatgpt-account-1" {
			t.Fatalf("unsafe or incomplete API call = %#v", call)
		}
	}

	restarted := NewApp(host, []byte("index"))
	restarted.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", dataDir, server.URL)))
	reloaded, errReload := restarted.accounts.List(context.Background(), ListQuery{Page: 1, PageSize: 50})
	if errReload != nil || len(reloaded.Accounts) != 1 || reloaded.Accounts[0].PlanType != "free" || reloaded.Accounts[0].Usage == nil || reloaded.Accounts[0].Usage.Codex == nil || reloaded.Accounts[0].Usage.Codex.ActiveResetCount == nil || *reloaded.Accounts[0].Usage.Codex.ActiveResetCount != 0 {
		restarted.Close()
		t.Fatalf("reloaded account = %#v err=%v", reloaded.Accounts, errReload)
	}
	restarted.Close()
	raw, errRead := os.ReadFile(usageStorePath(dataDir))
	if errRead != nil {
		t.Fatalf("read usage store: %v", errRead)
	}
	for _, forbidden := range []string{"management-secret", "Bearer $TOKEN$", "chatgpt-account-1", "rate_limit_reset_credits", "primary_window"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("usage store leaked %q: %s", forbidden, raw)
		}
	}
}

func TestQuotaMetadataBootstrapRunsWhenAccountListIsFirstOpened(t *testing.T) {
	var mu sync.Mutex
	requests := make([]managementAPICallRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		if errDecode := json.NewDecoder(request.Body).Decode(&call); errDecode != nil {
			t.Errorf("decode API call: %v", errDecode)
		}
		mu.Lock()
		requests = append(requests, call)
		mu.Unlock()
		body := `{"available_count":0,"credits":[]}`
		if call.URL == codexQuotaUsageURL {
			body = `{"plan_type":"Free"}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()

	app := NewApp(quotaMetadataHost(), []byte("index"))
	defer app.Close()
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", t.TempDir(), server.URL)))
	response := app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method: http.MethodGet, Path: "/v0/management/plugins/cpa-account-config-manager/accounts",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}},
		Query:   map[string][]string{"page": {"1"}, "page_size": {"50"}},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list accounts = %d %s", response.StatusCode, response.Body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := app.usage.Snapshot("auth-1")
		if snapshot != nil && snapshot.Codex != nil && !snapshot.Codex.MetadataObservedAt.IsZero() &&
			snapshot.Codex.ActiveResetCount != nil && *snapshot.Codex.ActiveResetCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quota metadata was not collected after opening the account list: %#v", snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}

	app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method: http.MethodGet, Path: "/v0/management/plugins/cpa-account-config-manager/accounts",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}},
	})
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0].URL != codexQuotaUsageURL || requests[1].URL != codexQuotaResetCreditsURL {
		t.Fatalf("bootstrap API calls = %#v", requests)
	}
}

func TestQuotaMetadataHTTP401BecomesSanitizedInspectionEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status_code": http.StatusUnauthorized,
			"header":      map[string][]string{},
			"body":        `{"detail":"upstream-secret-must-not-be-retained"}`,
		})
	}))
	defer server.Close()

	app := NewApp(quotaMetadataHost(), []byte("index"))
	defer app.Close()
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", t.TempDir(), server.URL)))
	account := Account{ID: "auth-1", AuthID: "auth-1", Provider: "codex", Type: "codex"}
	errRefresh := app.runNewAccountQuotaMetadata(t.Context(), account, "management-secret")
	var upstream quotaMetadataHTTPError
	if !errors.As(errRefresh, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("quota metadata error = %v", errRefresh)
	}
	app.inspection.mu.RLock()
	record := app.inspection.records["auth-1"]
	app.inspection.mu.RUnlock()
	if record.Signal.StatusCode != http.StatusUnauthorized || record.Signal.ReasonCode != "invalid_credentials" ||
		!record.Signal.AutoDisableEligible {
		t.Fatalf("inspection signal = %#v", record.Signal)
	}
	encoded, errMarshal := json.Marshal(record)
	if errMarshal != nil {
		t.Fatalf("marshal inspection results: %v", errMarshal)
	}
	if bytes.Contains(encoded, []byte("upstream-secret")) || bytes.Contains(encoded, []byte("management-secret")) {
		t.Fatalf("inspection evidence retained a secret: %s", encoded)
	}
}

func TestQuotaMetadataResetRequiresConfirmationAndRefreshesRemainingCount(t *testing.T) {
	host := quotaMetadataHost()
	var mu sync.Mutex
	resetReads := 0
	consumeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		_ = json.NewDecoder(request.Body).Decode(&call)
		body := `{"plan_type":"plus"}`
		mu.Lock()
		switch call.URL {
		case codexQuotaResetCreditsURL:
			resetReads++
			count := 1
			body = fmt.Sprintf(`{"available_count":%d,"credits":[]}`, count)
		case codexQuotaResetConsumeURL:
			consumeCalls++
			if call.Method != http.MethodPost || !strings.Contains(call.Data, "redeem_request_id") {
				t.Errorf("consume call = %#v", call)
			}
			body = `{}`
		}
		mu.Unlock()
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()
	app := NewApp(host, []byte("index"))
	defer app.Close()
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", t.TempDir(), server.URL)))

	request := cpaapi.ManagementRequest{
		Method: http.MethodPost, Path: "/v0/management/plugins/cpa-account-config-manager/accounts/quota-metadata/reset",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}}, Body: []byte(`{"account_id":"auth-1"}`),
	}
	denied := app.HandleManagement(context.Background(), request)
	if denied.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed reset = %d %s", denied.StatusCode, denied.Body)
	}
	mu.Lock()
	if consumeCalls != 0 || resetReads != 0 {
		mu.Unlock()
		t.Fatalf("unconfirmed request made upstream calls: reset=%d consume=%d", resetReads, consumeCalls)
	}
	mu.Unlock()

	request.Body = []byte(`{"account_id":"auth-1","confirm":true}`)
	response := app.HandleManagement(context.Background(), request)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("confirmed reset = %d %s", response.StatusCode, response.Body)
	}
	var result QuotaMetadataResponse
	_ = json.Unmarshal(response.Body, &result)
	if !result.ResetCreditUsed || result.ActiveResetCount == nil || *result.ActiveResetCount != 0 || result.Warning != "quota_metadata_refresh_after_reset_unavailable" {
		t.Fatalf("reset result = %#v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if consumeCalls != 1 || resetReads != 2 {
		t.Fatalf("upstream calls: reset=%d consume=%d", resetReads, consumeCalls)
	}
}

func quotaMetadataHost() *fakeAuthHost {
	path := filepath.Join(os.TempDir(), "auth-1.json")
	return &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "auth-1", Name: "auth-1.json", Provider: "codex", Type: "codex", AccountType: "oauth",
			PlanType: "k12", Email: "quota@example.com", Source: "file", Path: path,
		}},
		details: map[string]cpaapi.HostAuthGetResponse{
			"auth-1": {AuthIndex: "auth-1", Name: "auth-1.json", Path: path, JSON: json.RawMessage(`{"email":"quota@example.com","id_token":{"chatgpt_account_id":"chatgpt-account-1","plan_type":"k12"}}`)},
		},
	}
}

func TestAntigravityQuotaMetadataRefreshPersistsWindowsAndPlan(t *testing.T) {
	dataDir := t.TempDir()
	host := antigravityQuotaMetadataHost()
	var mu sync.Mutex
	requests := make([]managementAPICallRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		if errDecode := json.NewDecoder(request.Body).Decode(&call); errDecode != nil {
			t.Errorf("decode API call: %v", errDecode)
		}
		mu.Lock()
		requests = append(requests, call)
		mu.Unlock()
		body := `{"groups":[{"buckets":[{"remainingFraction":0.25,"window":"5h","resetTime":"2099-01-01T00:00:00Z"},{"remainingFraction":0.5,"window":"weekly","resetTime":"2099-01-08T00:00:00Z"}]}]}`
		if call.URL == antigravityLoadCodeAssistURL {
			body = `{"currentTier":{"id":"g1-ultra-lite-tier"}}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()

	app := NewApp(host, []byte("index"))
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", dataDir, server.URL)))
	response := app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method:  http.MethodPost,
		Path:    "/v0/management/plugins/cpa-account-config-manager/accounts/quota-metadata/refresh",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}},
		Body:    []byte(`{"account_id":"ag-1"}`),
	})
	if response.StatusCode != http.StatusOK {
		app.Close()
		t.Fatalf("refresh = %d %s", response.StatusCode, response.Body)
	}
	var refreshed QuotaMetadataResponse
	if errDecode := json.Unmarshal(response.Body, &refreshed); errDecode != nil {
		app.Close()
		t.Fatalf("decode response: %v", errDecode)
	}
	if refreshed.PlanType != "ultra-lite" || refreshed.ActiveResetCount != nil {
		app.Close()
		t.Fatalf("refresh result = %#v", refreshed)
	}
	listed, errList := app.accounts.List(context.Background(), ListQuery{Page: 1, PageSize: 50})
	if errList != nil || len(listed.Accounts) != 1 || listed.Accounts[0].PlanType != "ultra-lite" ||
		listed.Accounts[0].Usage == nil || listed.Accounts[0].Usage.Quota == nil ||
		listed.Accounts[0].Usage.Quota.FiveHour == nil || listed.Accounts[0].Usage.Quota.FiveHour.UsedPercent != 75 ||
		listed.Accounts[0].Usage.Codex != nil {
		app.Close()
		t.Fatalf("listed account = %#v err=%v", listed.Accounts, errList)
	}
	app.Close()

	mu.Lock()
	captured := append([]managementAPICallRequest(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 || captured[0].URL != antigravityQuotaURLs[0] || captured[1].URL != antigravityLoadCodeAssistURL {
		t.Fatalf("API calls = %#v", captured)
	}
	if captured[0].Header["User-Agent"] != antigravityQuotaUserAgent || captured[0].Data != `{"project":"gcp-project"}` {
		t.Fatalf("quota call = %#v", captured[0])
	}

	restarted := NewApp(host, []byte("index"))
	restarted.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", dataDir, server.URL)))
	reloaded, errReload := restarted.accounts.List(context.Background(), ListQuery{Page: 1, PageSize: 50})
	if errReload != nil || len(reloaded.Accounts) != 1 || reloaded.Accounts[0].PlanType != "ultra-lite" ||
		reloaded.Accounts[0].Usage == nil || reloaded.Accounts[0].Usage.Quota == nil ||
		reloaded.Accounts[0].Usage.Quota.SevenDay == nil || reloaded.Accounts[0].Usage.Quota.SevenDay.UsedPercent != 50 {
		restarted.Close()
		t.Fatalf("reloaded account = %#v err=%v", reloaded.Accounts, errReload)
	}
	restarted.Close()
}

func TestAntigravityQuotaMetadataResetRemainsUnsupported(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "body": `{}`})
	}))
	defer server.Close()
	app := NewApp(antigravityQuotaMetadataHost(), []byte("index"))
	defer app.Close()
	app.Configure([]byte(fmt.Sprintf("data_dir: %q\nmanagement_base_url: %q\n", t.TempDir(), server.URL)))
	response := app.HandleManagement(context.Background(), cpaapi.ManagementRequest{
		Method: http.MethodPost, Path: "/v0/management/plugins/cpa-account-config-manager/accounts/quota-metadata/reset",
		Headers: http.Header{"Authorization": []string{"Bearer management-secret"}},
		Body:    []byte(`{"account_id":"ag-1","confirm":true}`),
	})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reset = %d %s", response.StatusCode, response.Body)
	}
	if !bytes.Contains(response.Body, []byte(ErrQuotaMetadataUnsupported.Error())) {
		t.Fatalf("reset body = %s", response.Body)
	}
	if called {
		t.Fatal("antigravity reset called upstream")
	}
}

func antigravityQuotaMetadataHost() *fakeAuthHost {
	path := filepath.Join(os.TempDir(), "ag-1.json")
	return &fakeAuthHost{
		entries: []cpaapi.HostAuthFileEntry{{
			AuthIndex: "ag-1", Name: "ag-1.json", Provider: "antigravity", Type: "antigravity", AccountType: "oauth",
			ProjectID: "gcp-project", Email: "ag@example.com", Source: "file", Path: path,
		}},
		details: map[string]cpaapi.HostAuthGetResponse{
			"ag-1": {AuthIndex: "ag-1", Name: "ag-1.json", Path: path, JSON: json.RawMessage(`{"email":"ag@example.com","project_id":"gcp-project","type":"antigravity"}`)},
		},
	}
}

func TestQuotaMetadataPersistenceKeepsAccountsSeparatedByIdentity(t *testing.T) {
	tracker := NewUsageTracker()
	tracker.persistDelay = time.Hour
	defer tracker.Close()
	tracker.bindUsageAccounts([]cpaapi.HostAuthFileEntry{
		{AuthIndex: "left", Email: "left@example.com"},
		{AuthIndex: "right", Email: "right@example.com"},
	})
	zero := 0
	tracker.ObserveCredentialUsage("left", &CodexUsageSnapshot{PlanType: "free", ActiveResetCount: &zero})
	if right := tracker.Snapshot("right"); right != nil {
		t.Fatalf("right account received left metadata: %#v", right)
	}
	observedAt := time.Now().UTC()
	tracker.ObserveCredentialUsage("left", &CodexUsageSnapshot{MetadataObservedAt: observedAt})
	left := tracker.Snapshot("left")
	if left == nil || left.Codex == nil || left.Codex.PlanType != "" || left.Codex.ActiveResetCount != nil {
		t.Fatalf("explicit metadata refresh retained stale values: %#v", left)
	}
}
