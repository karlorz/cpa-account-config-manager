package manager

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestIsKimiAccount(t *testing.T) {
	tests := []struct {
		account Account
		want    bool
	}{
		{account: Account{Provider: "kimi"}, want: true},
		{account: Account{Provider: "Kimi"}, want: true},
		{account: Account{Type: " KIMI "}, want: true},
		{account: Account{Provider: "openai-compatible-kimi"}, want: false},
		{account: Account{Type: "openai-compatible-kimi"}, want: false},
		{account: Account{Provider: "antigravity"}, want: false},
		{account: Account{Provider: "codex"}, want: false},
		{account: Account{}, want: false},
	}

	for _, tc := range tests {
		if got := isKimiAccount(tc.account); got != tc.want {
			t.Errorf("isKimiAccount(%+v) = %v, want %v", tc.account, got, tc.want)
		}
	}
}

func TestParseKimiUsagePayloadFiveHourAndWeekly(t *testing.T) {
	raw := []byte(`{
		"usage": { "used": 45, "limit": 100, "reset_at": "2026-08-22T21:26:00Z", "name": "Weekly limit" },
		"limits": [
			{
				"name": "5h limit",
				"detail": { "used": 18, "limit": 100, "reset_at": "2026-08-18T19:26:00Z" },
				"window": { "duration": 5, "timeUnit": "HOURS" }
			}
		]
	}`)

	snapshot, ok := parseKimiUsagePayload(raw)
	if !ok || snapshot == nil {
		t.Fatalf("parseKimiUsagePayload returned ok=%v, snapshot=%#v", ok, snapshot)
	}

	if snapshot.Provider != "kimi" {
		t.Errorf("Provider = %q, want %q", snapshot.Provider, "kimi")
	}
	if snapshot.PlanType != "" {
		t.Errorf("PlanType = %q, want empty", snapshot.PlanType)
	}

	if snapshot.FiveHour == nil {
		t.Fatal("FiveHour window is nil")
	}
	if snapshot.FiveHour.UsedPercent != 18 {
		t.Errorf("FiveHour.UsedPercent = %v, want 18", snapshot.FiveHour.UsedPercent)
	}
	if snapshot.FiveHour.WindowMinutes != 300 {
		t.Errorf("FiveHour.WindowMinutes = %v, want 300", snapshot.FiveHour.WindowMinutes)
	}
	expectedFiveHourReset := time.Date(2026, time.August, 18, 19, 26, 0, 0, time.UTC)
	if snapshot.FiveHour.ResetAt == nil || !snapshot.FiveHour.ResetAt.Equal(expectedFiveHourReset) {
		t.Errorf("FiveHour.ResetAt = %v, want %v", snapshot.FiveHour.ResetAt, expectedFiveHourReset)
	}

	if snapshot.SevenDay == nil {
		t.Fatal("SevenDay window is nil")
	}
	if snapshot.SevenDay.UsedPercent != 45 {
		t.Errorf("SevenDay.UsedPercent = %v, want 45", snapshot.SevenDay.UsedPercent)
	}
	if snapshot.SevenDay.WindowMinutes != 10080 {
		t.Errorf("SevenDay.WindowMinutes = %v, want 10080", snapshot.SevenDay.WindowMinutes)
	}
	expectedWeeklyReset := time.Date(2026, time.August, 22, 21, 26, 0, 0, time.UTC)
	if snapshot.SevenDay.ResetAt == nil || !snapshot.SevenDay.ResetAt.Equal(expectedWeeklyReset) {
		t.Errorf("SevenDay.ResetAt = %v, want %v", snapshot.SevenDay.ResetAt, expectedWeeklyReset)
	}
}

func TestParseKimiUsagePayloadProtoTimeUnitMinuteFiveHour(t *testing.T) {
	raw := []byte(`{
		"usage": { "limit": "100", "used": "56", "remaining": "44", "resetTime": "2026-08-22T12:26:13.608898Z" },
		"limits": [
			{
				"window": { "duration": 300, "timeUnit": "TIME_UNIT_MINUTE" },
				"detail": { "limit": "100", "used": "73", "remaining": "27", "resetTime": "2026-08-18T10:26:13.608898Z" }
			}
		]
	}`)

	snapshot, ok := parseKimiUsagePayload(raw)
	if !ok || snapshot == nil {
		t.Fatalf("parseKimiUsagePayload returned ok=%v, snapshot=%#v", ok, snapshot)
	}

	if snapshot.FiveHour == nil {
		t.Fatal("FiveHour window is nil")
	}
	if snapshot.FiveHour.UsedPercent != 73 {
		t.Errorf("FiveHour.UsedPercent = %v, want 73", snapshot.FiveHour.UsedPercent)
	}
	if snapshot.FiveHour.WindowMinutes != 300 {
		t.Errorf("FiveHour.WindowMinutes = %v, want 300", snapshot.FiveHour.WindowMinutes)
	}
	expectedFiveHourReset, err := time.Parse(time.RFC3339Nano, "2026-08-18T10:26:13.608898Z")
	if err != nil {
		t.Fatalf("parse expectedFiveHourReset: %v", err)
	}
	if snapshot.FiveHour.ResetAt == nil || !snapshot.FiveHour.ResetAt.Equal(expectedFiveHourReset) {
		t.Errorf("FiveHour.ResetAt = %v, want %v", snapshot.FiveHour.ResetAt, expectedFiveHourReset)
	}

	if snapshot.SevenDay == nil {
		t.Fatal("SevenDay window is nil")
	}
	if math.Abs(snapshot.SevenDay.UsedPercent-56) > 0.0001 {
		t.Errorf("SevenDay.UsedPercent = %v, want 56", snapshot.SevenDay.UsedPercent)
	}
	if snapshot.SevenDay.WindowMinutes != 10080 {
		t.Errorf("SevenDay.WindowMinutes = %v, want 10080", snapshot.SevenDay.WindowMinutes)
	}
	expectedWeeklyReset, err := time.Parse(time.RFC3339Nano, "2026-08-22T12:26:13.608898Z")
	if err != nil {
		t.Fatalf("parse expectedWeeklyReset: %v", err)
	}
	if snapshot.SevenDay.ResetAt == nil || !snapshot.SevenDay.ResetAt.Equal(expectedWeeklyReset) {
		t.Errorf("SevenDay.ResetAt = %v, want %v", snapshot.SevenDay.ResetAt, expectedWeeklyReset)
	}
}

func TestParseKimiUsagePayloadRemainingOnly(t *testing.T) {
	raw := []byte(`{
		"limits": [
			{
				"name": "5h",
				"detail": { "remaining": 82, "limit": 100 },
				"window": { "duration": 5, "timeUnit": "HOURS" }
			}
		]
	}`)

	snapshot, ok := parseKimiUsagePayload(raw)
	if !ok || snapshot == nil || snapshot.FiveHour == nil {
		t.Fatalf("parseKimiUsagePayload returned ok=%v, snapshot=%#v", ok, snapshot)
	}

	if snapshot.FiveHour.UsedPercent != 18 {
		t.Errorf("FiveHour.UsedPercent = %v, want 18", snapshot.FiveHour.UsedPercent)
	}
	if snapshot.FiveHour.WindowMinutes != 300 {
		t.Errorf("FiveHour.WindowMinutes = %v, want 300", snapshot.FiveHour.WindowMinutes)
	}
}

func TestParseKimiUsagePayloadResetFieldVariants(t *testing.T) {
	stubNow := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	oldNow := kimiQuotaNow
	kimiQuotaNow = func() time.Time { return stubNow }
	defer func() { kimiQuotaNow = oldNow }()

	// Test snake_case and camelCase reset time fields, and relative TTL
	rawCamel := []byte(`{
		"limits": [
			{
				"name": "5h",
				"detail": { "used": 10, "limit": 100, "resetAt": "2026-08-18T17:00:00Z" }
			},
			{
				"name": "weekly",
				"detail": { "used": 20, "limit": 100, "resetTime": "2026-08-25T12:00:00.500Z" }
			}
		]
	}`)
	snapshotCamel, okCamel := parseKimiUsagePayload(rawCamel)
	if !okCamel || snapshotCamel == nil || snapshotCamel.FiveHour == nil || snapshotCamel.SevenDay == nil {
		t.Fatalf("parse camel failed: ok=%v, snapshot=%#v", okCamel, snapshotCamel)
	}
	if expected := time.Date(2026, time.August, 18, 17, 0, 0, 0, time.UTC); snapshotCamel.FiveHour.ResetAt == nil || !snapshotCamel.FiveHour.ResetAt.Equal(expected) {
		t.Errorf("camel FiveHour.ResetAt = %v, want %v", snapshotCamel.FiveHour.ResetAt, expected)
	}
	if expected := time.Date(2026, time.August, 25, 12, 0, 0, 500000000, time.UTC); snapshotCamel.SevenDay.ResetAt == nil || !snapshotCamel.SevenDay.ResetAt.Equal(expected) {
		t.Errorf("camel SevenDay.ResetAt = %v, want %v", snapshotCamel.SevenDay.ResetAt, expected)
	}

	rawTTL := []byte(`{
		"limits": [
			{
				"name": "5h",
				"detail": { "used": 10, "limit": 100, "reset_in": 3600 }
			}
		]
	}`)
	snapshotTTL, okTTL := parseKimiUsagePayload(rawTTL)
	if !okTTL || snapshotTTL == nil || snapshotTTL.FiveHour == nil {
		t.Fatalf("parse TTL failed: ok=%v, snapshot=%#v", okTTL, snapshotTTL)
	}
	expectedTTLReset := stubNow.Add(3600 * time.Second)
	if snapshotTTL.FiveHour.ResetAt == nil || !snapshotTTL.FiveHour.ResetAt.Equal(expectedTTLReset) {
		t.Errorf("TTL FiveHour.ResetAt = %v, want %v", snapshotTTL.FiveHour.ResetAt, expectedTTLReset)
	}
}

func TestParseKimiUsagePayloadTwoFiveHourClassRowsSelectsWorst(t *testing.T) {
	raw := []byte(`{
		"limits": [
			{
				"name": "5h fast",
				"detail": { "used": 10, "limit": 100 },
				"window": { "duration": 5, "timeUnit": "HOURS" }
			},
			{
				"name": "5h general",
				"detail": { "used": 80, "limit": 100 },
				"window": { "duration": 5, "timeUnit": "HOURS" }
			}
		]
	}`)

	snapshot, ok := parseKimiUsagePayload(raw)
	if !ok || snapshot == nil || snapshot.FiveHour == nil {
		t.Fatalf("parseKimiUsagePayload returned ok=%v, snapshot=%#v", ok, snapshot)
	}
	if snapshot.FiveHour.UsedPercent != 80 {
		t.Errorf("FiveHour.UsedPercent = %v, want 80 (worst)", snapshot.FiveHour.UsedPercent)
	}
}

func TestParseKimiUsagePayloadDailyAndMonthlyClassification(t *testing.T) {
	raw := []byte(`{
		"limits": [
			{
				"name": "daily limit",
				"detail": { "used": 30, "limit": 100 },
				"window": { "duration": 1, "timeUnit": "DAYS" }
			},
			{
				"name": "monthly limit",
				"detail": { "used": 50, "limit": 100 },
				"window": { "duration": 30, "timeUnit": "DAYS" }
			}
		]
	}`)

	snapshot, ok := parseKimiUsagePayload(raw)
	if !ok || snapshot == nil {
		t.Fatalf("parseKimiUsagePayload returned ok=%v, snapshot=%#v", ok, snapshot)
	}

	if snapshot.FiveHour == nil {
		t.Fatal("daily should map to FiveHour")
	}
	if snapshot.FiveHour.UsedPercent != 30 || snapshot.FiveHour.WindowMinutes != 1440 {
		t.Errorf("daily FiveHour = %#v, want UsedPercent=30, WindowMinutes=1440", snapshot.FiveHour)
	}

	if snapshot.SevenDay == nil {
		t.Fatal("monthly should map to SevenDay")
	}
	if snapshot.SevenDay.UsedPercent != 50 || snapshot.SevenDay.WindowMinutes != 43200 {
		t.Errorf("monthly SevenDay = %#v, want UsedPercent=50, WindowMinutes=43200", snapshot.SevenDay)
	}
}

func TestParseKimiUsagePayloadUnknownAndEmptyDropped(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty object", raw: `{}`},
		{name: "empty limits", raw: `{"limits": []}`},
		{name: "unlabeled with no duration", raw: `{"limits": [{"name": "unknown_bucket", "detail": {"used": 10, "limit": 100}}]}`},
		{name: "invalid json", raw: `not json`},
		{name: "missing limit", raw: `{"limits": [{"name": "5h", "detail": {"used": 10}}]}`},
		{name: "missing both used and remaining", raw: `{"limits": [{"name": "5h", "detail": {"limit": 100}}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, ok := parseKimiUsagePayload([]byte(tc.raw))
			if ok || snapshot != nil {
				t.Errorf("expected ok=false and nil snapshot for %s, got ok=%v, snapshot=%#v", tc.name, ok, snapshot)
			}
		})
	}
}

func TestClassifyKimiWindowProtoTimeUnits(t *testing.T) {
	tests := []struct {
		name        string
		duration    float64
		timeUnit    string
		wantKind    kimiWindowKind
		wantMinutes int
	}{
		{
			name:        "proto minutes five hour",
			duration:    300,
			timeUnit:    "TIME_UNIT_MINUTE",
			wantKind:    kimiWindowFiveHour,
			wantMinutes: 300,
		},
		{
			name:        "proto hour five hour",
			duration:    5,
			timeUnit:    "TIME_UNIT_HOUR",
			wantKind:    kimiWindowFiveHour,
			wantMinutes: 300,
		},
		{
			name:        "proto day weekly",
			duration:    7,
			timeUnit:    "TIME_UNIT_DAY",
			wantKind:    kimiWindowWeekly,
			wantMinutes: 10080,
		},
		{
			name:        "proto day daily",
			duration:    1,
			timeUnit:    "TIME_UNIT_DAY",
			wantKind:    kimiWindowDaily,
			wantMinutes: 1440,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			windowMap := map[string]any{
				"duration": tc.duration,
				"timeUnit": tc.timeUnit,
			}
			kind, minutes := classifyKimiWindow(nil, windowMap)
			if kind != tc.wantKind || minutes != tc.wantMinutes {
				t.Errorf("classifyKimiWindow(nil, %+v) = (%v, %v), want (%v, %v)",
					windowMap, kind, minutes, tc.wantKind, tc.wantMinutes)
			}
		})
	}
}

func TestFetchKimiQuotaMetadataSuccess(t *testing.T) {
	var mu sync.Mutex
	var capturedCall managementAPICallRequest

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call managementAPICallRequest
		if errDecode := json.NewDecoder(request.Body).Decode(&call); errDecode != nil {
			t.Errorf("decode API call: %v", errDecode)
		}
		mu.Lock()
		capturedCall = call
		mu.Unlock()

		body := `{
			"usage": { "used": 45, "limit": 100, "reset_at": "2026-08-22T21:26:00Z", "name": "Weekly limit" },
			"limits": [
				{
					"name": "5h limit",
					"detail": { "used": 18, "limit": 100, "reset_at": "2026-08-18T19:26:00Z" },
					"window": { "duration": 5, "timeUnit": "HOURS" }
				}
			]
		}`
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status_code": http.StatusOK,
			"header":      map[string][]string{},
			"body":        body,
		})
	}))
	defer server.Close()

	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()

	account := Account{ID: "kimi-acc-1", Provider: "kimi"}
	metadata, errFetch := fetchKimiQuotaMetadata(context.Background(), client, account)
	if errFetch != nil {
		t.Fatalf("fetchKimiQuotaMetadata failed: %v", errFetch)
	}

	mu.Lock()
	call := capturedCall
	mu.Unlock()

	if call.AuthIndex != "kimi-acc-1" {
		t.Errorf("AuthIndex = %q, want %q", call.AuthIndex, "kimi-acc-1")
	}
	if call.Method != http.MethodGet {
		t.Errorf("Method = %q, want %q", call.Method, http.MethodGet)
	}
	if call.URL != "https://api.kimi.com/coding/v1/usages" {
		t.Errorf("URL = %q, want https://api.kimi.com/coding/v1/usages", call.URL)
	}
	if call.Header["Authorization"] != "Bearer $TOKEN$" {
		t.Errorf("Authorization header = %q, want %q", call.Header["Authorization"], "Bearer $TOKEN$")
	}
	if len(call.Header) != 1 {
		t.Errorf("Header map has extra keys: %#v", call.Header)
	}

	if metadata.quota == nil {
		t.Fatal("metadata.quota is nil")
	}
	if metadata.quota.Provider != "kimi" {
		t.Errorf("metadata.quota.Provider = %q, want %q", metadata.quota.Provider, "kimi")
	}
	if metadata.quota.FiveHour == nil || metadata.quota.FiveHour.UsedPercent != 18 {
		t.Errorf("metadata.quota.FiveHour = %#v", metadata.quota.FiveHour)
	}
	if metadata.quota.SevenDay == nil || metadata.quota.SevenDay.UsedPercent != 45 {
		t.Errorf("metadata.quota.SevenDay = %#v", metadata.quota.SevenDay)
	}
}

func TestFetchKimiQuotaMetadata401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status_code": http.StatusUnauthorized,
			"header":      map[string][]string{},
			"body":        `{"error": "unauthorized"}`,
		})
	}))
	defer server.Close()

	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()

	account := Account{ID: "kimi-acc-1", Provider: "kimi"}
	_, errFetch := fetchKimiQuotaMetadata(context.Background(), client, account)
	if errFetch == nil {
		t.Fatal("expected error, got nil")
	}

	var httpErr quotaMetadataHTTPError
	if !errors.As(errFetch, &httpErr) {
		t.Fatalf("expected quotaMetadataHTTPError, got %T: %v", errFetch, errFetch)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("httpErr.StatusCode = %d, want %d", httpErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestFetchKimiQuotaMetadataEmptyBodyReturnsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status_code": http.StatusOK,
			"header":      map[string][]string{},
			"body":        `{}`,
		})
	}))
	defer server.Close()

	client, errClient := newManagementClient(server.URL, "management-secret", nil)
	if errClient != nil {
		t.Fatalf("management client: %v", errClient)
	}
	defer client.clearSecrets()

	account := Account{ID: "kimi-acc-1", Provider: "kimi"}
	_, errFetch := fetchKimiQuotaMetadata(context.Background(), client, account)
	if !errors.Is(errFetch, ErrQuotaMetadataUnavailable) {
		t.Fatalf("expected ErrQuotaMetadataUnavailable, got: %v", errFetch)
	}
}
