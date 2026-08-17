package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseAntigravityQuotaSummaryRemainingFractionAndSnakeCase(t *testing.T) {
	snapshot, ok := parseAntigravityQuotaSummary([]byte(`{
		"groups": [{
			"display_name": "Gemini",
			"buckets": [
				{"remaining_fraction": 1, "window": "5h", "reset_time": "2026-08-17T15:00:00Z"},
				{"remaining_fraction": 0, "window": "weekly", "reset_time": "2026-08-24T15:00:00.123456789012Z"}
			]
		}]
	}`))
	if !ok || snapshot == nil || snapshot.FiveHour == nil || snapshot.SevenDay == nil {
		t.Fatalf("snapshot = %#v ok=%t", snapshot, ok)
	}
	if snapshot.FiveHour.UsedPercent != 0 || snapshot.FiveHour.WindowMinutes != 300 {
		t.Fatalf("5h window = %#v", snapshot.FiveHour)
	}
	if snapshot.SevenDay.UsedPercent != 100 || snapshot.SevenDay.WindowMinutes != 10080 {
		t.Fatalf("weekly window = %#v", snapshot.SevenDay)
	}
	if snapshot.SevenDay.ResetAt == nil || !snapshot.SevenDay.ResetAt.Equal(time.Date(2026, time.August, 24, 15, 0, 0, 123456789, time.UTC)) {
		t.Fatalf("weekly reset = %v", snapshot.SevenDay.ResetAt)
	}
}

func TestParseAntigravityQuotaSummarySelectsWorstGroupAndMonthlyLongWindow(t *testing.T) {
	snapshot, ok := parseAntigravityQuotaSummary([]byte(`{
		"groups": [
			{"displayName": "Gemini", "buckets": [
				{"remainingFraction": 0.9, "window": "five_hour", "resetTime": "2026-08-17T16:00:00Z"},
				{"remainingFraction": 0.6, "window": "monthly", "resetTime": "2026-09-01T00:00:00Z"}
			]},
			{"display_name": "Claude", "buckets": [
				{"remaining_fraction": 0.2, "window": "five-hour", "reset_time": "2026-08-17T14:00:00Z"},
				{"remaining_fraction": 0.3, "window": "month", "reset_time": "2026-09-10T00:00:00Z"}
			]}
		]
	}`))
	if !ok || snapshot == nil || snapshot.FiveHour == nil || snapshot.SevenDay == nil {
		t.Fatalf("snapshot = %#v ok=%t", snapshot, ok)
	}
	if snapshot.FiveHour.UsedPercent != 80 || snapshot.FiveHour.WindowMinutes != 300 {
		t.Fatalf("worst 5h = %#v", snapshot.FiveHour)
	}
	if snapshot.SevenDay.UsedPercent != 70 || snapshot.SevenDay.WindowMinutes != 43200 {
		t.Fatalf("worst monthly = %#v", snapshot.SevenDay)
	}
}

func TestParseAntigravityQuotaSummaryPrefersWeeklyOverMonthly(t *testing.T) {
	snapshot, ok := parseAntigravityQuotaSummary([]byte(`{
		"groups": [{"buckets": [
			{"remainingFraction": 0.75, "window": "week"},
			{"remainingFraction": 0.1, "window": "monthly"}
		]}]
	}`))
	if !ok || snapshot == nil || snapshot.SevenDay == nil {
		t.Fatalf("snapshot = %#v ok=%t", snapshot, ok)
	}
	if snapshot.SevenDay.UsedPercent != 25 || snapshot.SevenDay.WindowMinutes != 10080 {
		t.Fatalf("weekly should win over monthly = %#v", snapshot.SevenDay)
	}
}

func TestParseAntigravityQuotaSummaryUsesDailyWhenFiveHourMissing(t *testing.T) {
	snapshot, ok := parseAntigravityQuotaSummary([]byte(`{
		"groups": [{"buckets": [
			{"remainingFraction": 0.5, "window": "daily"},
			{"remainingFraction": 0.75, "window": "weekly"}
		]}]
	}`))
	if !ok || snapshot == nil || snapshot.FiveHour == nil || snapshot.SevenDay == nil {
		t.Fatalf("snapshot = %#v ok=%t", snapshot, ok)
	}
	if snapshot.FiveHour.UsedPercent != 50 {
		t.Fatalf("daily fallback = %#v", snapshot.FiveHour)
	}
	if snapshot.SevenDay.UsedPercent != 25 {
		t.Fatalf("weekly = %#v", snapshot.SevenDay)
	}
}

func TestParseAntigravityQuotaSummaryRejectsEmptyGroups(t *testing.T) {
	for _, raw := range []string{`{}`, `{"groups":[]}`, `{"groups":[{"buckets":[]}]}`, `{"groups":null}`} {
		if snapshot, ok := parseAntigravityQuotaSummary([]byte(raw)); ok || snapshot != nil {
			t.Fatalf("empty groups %s parsed as %#v", raw, snapshot)
		}
	}
}

func TestFetchAntigravityQuotaTriesDailyThenSandboxThenProd(t *testing.T) {
	var mu sync.Mutex
	calls := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		if errDecode := json.NewDecoder(request.Body).Decode(&call); errDecode != nil {
			t.Errorf("decode API call: %v", errDecode)
		}
		mu.Lock()
		calls = append(calls, call.URL)
		mu.Unlock()
		status := http.StatusOK
		body := `{"groups":[{"buckets":[{"remainingFraction":0.4,"window":"5h"},{"remainingFraction":0.7,"window":"weekly"}]}]}`
		switch call.URL {
		case antigravityQuotaURLs[0]:
			status = http.StatusForbidden
			body = `{"error":"forbidden"}`
		case antigravityQuotaURLs[1]:
			status = http.StatusNotFound
			body = `{"error":"missing"}`
		case antigravityLoadCodeAssistURL:
			body = `{"currentTier":{"id":"g1-pro-tier"}}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": status, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()

	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()
	metadata, errFetch := fetchAntigravityQuotaMetadata(context.Background(), client, Account{ID: "ag-1", Provider: "antigravity"}, map[string]any{"project_id": "gcp-project"})
	if errFetch != nil {
		t.Fatalf("fetch: %v", errFetch)
	}
	if metadata.planType != "pro" || metadata.quota == nil || metadata.quota.FiveHour == nil || metadata.quota.FiveHour.UsedPercent != 60 {
		t.Fatalf("metadata = %#v", metadata)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 4 || calls[0] != antigravityQuotaURLs[0] || calls[1] != antigravityQuotaURLs[1] || calls[2] != antigravityQuotaURLs[2] || calls[3] != antigravityLoadCodeAssistURL {
		t.Fatalf("URL order = %#v", calls)
	}
}

func TestFetchAntigravityQuotaFirstParseable2xxWins(t *testing.T) {
	var mu sync.Mutex
	calls := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		_ = json.NewDecoder(request.Body).Decode(&call)
		mu.Lock()
		calls = append(calls, call.URL)
		mu.Unlock()
		status := http.StatusOK
		body := `{"groups":[]}`
		if call.URL == antigravityQuotaURLs[1] {
			body = `{"groups":[{"buckets":[{"remainingFraction":1,"window":"5h"}]}]}`
		}
		if call.URL == antigravityLoadCodeAssistURL {
			body = `{"paidTier":{"id":"free-tier"}}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": status, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()

	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()
	metadata, errFetch := fetchAntigravityQuotaMetadata(context.Background(), client, Account{ID: "ag-1", ProjectID: "from-account"}, nil)
	if errFetch != nil || metadata.quota == nil || metadata.quota.FiveHour == nil || metadata.quota.FiveHour.UsedPercent != 0 || metadata.planType != "free" {
		t.Fatalf("metadata = %#v err=%v", metadata, errFetch)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, url := range calls {
		if url == antigravityQuotaURLs[2] {
			t.Fatalf("prod URL was called after a parseable sandbox body: %#v", calls)
		}
	}
}

func TestFetchAntigravityQuotaMissingProjectIDDoesNotCallAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": 200, "body": `{}`})
	}))
	defer server.Close()
	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()
	_, errFetch := fetchAntigravityQuotaMetadata(context.Background(), client, Account{ID: "ag-1", Provider: "antigravity"}, nil)
	if !errors.Is(errFetch, errAntigravityProjectIDRequired) {
		t.Fatalf("error = %v, want project_id required", errFetch)
	}
	if called {
		t.Fatal("missing project_id called Cloud Code")
	}
}

func TestFetchAntigravityQuotaLoadCodeAssistFailureStillReturnsWindows(t *testing.T) {
	var mu sync.Mutex
	var captured []managementAPICallRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		_ = json.NewDecoder(request.Body).Decode(&call)
		mu.Lock()
		captured = append(captured, call)
		mu.Unlock()
		status := http.StatusOK
		body := `{"groups":[{"buckets":[{"remainingFraction":0,"window":"5h"},{"remainingFraction":0.5,"window":"weekly"}]}]}`
		if call.URL == antigravityLoadCodeAssistURL {
			status = http.StatusInternalServerError
			body = `{"error":"unavailable"}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status_code": status, "header": map[string][]string{}, "body": body})
	}))
	defer server.Close()
	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()
	metadata, errFetch := fetchAntigravityQuotaMetadata(context.Background(), client, Account{ID: "ag-1", ProjectID: "proj-123"}, nil)
	if errFetch != nil || metadata.planType != "" || metadata.quota == nil || metadata.quota.FiveHour == nil || metadata.quota.FiveHour.UsedPercent != 100 {
		t.Fatalf("metadata = %#v err=%v", metadata, errFetch)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(captured) < 2 {
		t.Fatalf("calls = %#v", captured)
	}
	quotaCall := captured[0]
	if quotaCall.Method != http.MethodPost || quotaCall.Header["Authorization"] != "Bearer $TOKEN$" ||
		quotaCall.Header["User-Agent"] != antigravityQuotaUserAgent || quotaCall.Header["Content-Type"] != "application/json" ||
		quotaCall.Data != `{"project":"proj-123"}` {
		t.Fatalf("quota call = %#v", quotaCall)
	}
	assist := captured[len(captured)-1]
	if assist.URL != antigravityLoadCodeAssistURL || !strings.Contains(assist.Data, `"ideType":"ANTIGRAVITY"`) {
		t.Fatalf("loadCodeAssist call = %#v", assist)
	}
}

func TestResolveAntigravityProjectIDPrefersAccountThenDocument(t *testing.T) {
	if got := resolveAntigravityProjectID(Account{ProjectID: "account-project"}, map[string]any{"project_id": "meta-project"}); got != "account-project" {
		t.Fatalf("account project = %q", got)
	}
	if got := resolveAntigravityProjectID(Account{}, map[string]any{
		"attributes": map[string]any{"gemini_virtual_project": "virtual-project"},
	}); got != "virtual-project" {
		t.Fatalf("attributes project = %q", got)
	}
	if got := resolveAntigravityProjectID(Account{}, map[string]any{
		"installed": map[string]any{"project_id": "installed-project"},
	}); got != "installed-project" {
		t.Fatalf("installed project = %q", got)
	}
}
