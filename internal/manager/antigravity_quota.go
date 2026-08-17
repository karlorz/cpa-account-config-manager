package manager

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	antigravityQuotaUserAgent    = "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)"
	antigravityLoadCodeAssistURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	antigravityFiveHourMinutes   = 5 * 60
	antigravityDailyMinutes      = 24 * 60
	antigravityWeeklyMinutes     = 7 * 24 * 60
	antigravityMonthlyMinutes    = 30 * 24 * 60
)

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

var errAntigravityProjectIDRequired = errors.New("antigravity project_id is required")

type antigravityWindowKind int

const (
	antigravityWindowUnknown antigravityWindowKind = iota
	antigravityWindowFiveHour
	antigravityWindowDaily
	antigravityWindowWeekly
	antigravityWindowMonthly
)

func isAntigravityAccount(account Account) bool {
	return strings.EqualFold(strings.TrimSpace(firstNonEmpty(account.Provider, account.Type)), "antigravity")
}

func (a *App) fetchAntigravityQuotaMetadata(ctx context.Context, client *managementClient, account Account) (quotaMetadata, error) {
	var documentMetadata map[string]any
	if strings.TrimSpace(account.ProjectID) == "" && a != nil && a.accounts != nil {
		if document, errDocument := a.accounts.CurrentAuthDocument(ctx, account); errDocument == nil {
			documentMetadata = document.Metadata
		}
	}
	return fetchAntigravityQuotaMetadata(ctx, client, account, documentMetadata)
}

func fetchAntigravityQuotaMetadata(ctx context.Context, client *managementClient, account Account, documentMetadata map[string]any) (quotaMetadata, error) {
	projectID := resolveAntigravityProjectID(account, documentMetadata)
	if projectID == "" {
		return quotaMetadata{}, errAntigravityProjectIDRequired
	}
	headers := antigravityQuotaHeaders()
	body, errMarshal := json.Marshal(map[string]string{"project": projectID})
	if errMarshal != nil {
		return quotaMetadata{}, fmtAntigravityUnavailable(errMarshal)
	}
	projectID = ""
	var lastHTTP quotaMetadataHTTPError
	var sawHTTP bool
	// Official CPA #/quota tries daily, then sandbox, then prod; 403/404 continue.
	for _, quotaURL := range antigravityQuotaURLs {
		response, errCall := client.APICall(ctx, managementAPICallRequest{
			AuthIndex: account.ID, Method: http.MethodPost, URL: quotaURL, Header: headers, Data: string(body),
		})
		if errCall != nil {
			return quotaMetadata{}, ErrQuotaMetadataUnavailable
		}
		status := boundedHTTPStatus(response.StatusCode)
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			snapshot, ok := parseAntigravityQuotaSummary(response.Body)
			if !ok {
				continue
			}
			metadata := quotaMetadata{quota: snapshot}
			if planType := fetchAntigravityPlanType(ctx, client, account, headers); planType != "" {
				metadata.planType = planType
			}
			return metadata, nil
		}
		if status == http.StatusForbidden || status == http.StatusNotFound {
			continue
		}
		lastHTTP = quotaMetadataHTTPError{StatusCode: status}
		sawHTTP = true
		if status == http.StatusUnauthorized {
			return quotaMetadata{}, lastHTTP
		}
		return quotaMetadata{}, lastHTTP
	}
	if sawHTTP {
		return quotaMetadata{}, lastHTTP
	}
	return quotaMetadata{}, ErrQuotaMetadataUnavailable
}

func fetchAntigravityPlanType(ctx context.Context, client *managementClient, account Account, headers map[string]string) string {
	response, errCall := client.APICall(ctx, managementAPICallRequest{
		AuthIndex: account.ID, Method: http.MethodPost, URL: antigravityLoadCodeAssistURL, Header: headers,
		Data: `{"metadata":{"ideType":"ANTIGRAVITY"}}`,
	})
	if errCall != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ""
	}
	return parseAntigravityPlan(response.Body)
}

func antigravityQuotaHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    antigravityQuotaUserAgent,
	}
}

func resolveAntigravityProjectID(account Account, metadata map[string]any) string {
	if projectID := safeQuotaAccountID(account.ProjectID); projectID != "" {
		return projectID
	}
	if projectID := firstProjectID(metadata, "project_id", "projectId"); projectID != "" {
		return projectID
	}
	if attributes := anyMap(firstMapValue(metadata, "attributes")); attributes != nil {
		if projectID := firstProjectID(attributes, "project_id", "projectId", "gemini_virtual_project"); projectID != "" {
			return projectID
		}
	}
	if installed := anyMap(firstMapValue(metadata, "installed")); installed != nil {
		if projectID := firstProjectID(installed, "project_id", "projectId"); projectID != "" {
			return projectID
		}
	}
	if web := anyMap(firstMapValue(metadata, "web")); web != nil {
		if projectID := firstProjectID(web, "project_id", "projectId"); projectID != "" {
			return projectID
		}
	}
	return ""
}

func anyMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = item
		}
		return converted
	default:
		return nil
	}
}

func firstProjectID(values map[string]any, keys ...string) string {
	return safeQuotaAccountID(firstMapValue(values, keys...))
}

func parseAntigravityQuotaSummary(raw []byte) (*QuotaUsageSnapshot, bool) {
	object, ok := decodeQuotaObject(raw)
	if !ok {
		return nil, false
	}
	groups, _ := object["groups"].([]any)
	if len(groups) == 0 {
		return nil, false
	}
	var fiveHour, daily, weekly, monthly *UsageWindowSnapshot
	for _, groupValue := range groups {
		group, _ := groupValue.(map[string]any)
		if group == nil {
			continue
		}
		buckets, _ := firstMapValue(group, "buckets").([]any)
		for _, bucketValue := range buckets {
			bucket, _ := bucketValue.(map[string]any)
			if bucket == nil {
				continue
			}
			// remainingFraction is remaining quota; used_percent = (1 - remaining) * 100.
			usedPercent, valid := remainingFractionUsedPercent(firstMapValue(bucket, "remainingFraction", "remaining_fraction"))
			if !valid {
				continue
			}
			kind := classifyAntigravityWindow(
				quotaString(firstMapValue(bucket, "window")),
				quotaString(firstMapValue(bucket, "displayName", "display_name")),
				quotaString(firstMapValue(bucket, "bucketId", "bucket_id")),
			)
			window := &UsageWindowSnapshot{
				UsedPercent:   usedPercent,
				ResetAt:       parseAntigravityResetTime(quotaString(firstMapValue(bucket, "resetTime", "reset_time"))),
				WindowMinutes: antigravityWindowMinutes(kind),
			}
			switch kind {
			case antigravityWindowFiveHour:
				fiveHour = worseUsageWindow(fiveHour, window)
			case antigravityWindowDaily:
				daily = worseUsageWindow(daily, window)
			case antigravityWindowWeekly:
				weekly = worseUsageWindow(weekly, window)
			case antigravityWindowMonthly:
				monthly = worseUsageWindow(monthly, window)
			}
		}
	}
	snapshot := &QuotaUsageSnapshot{Provider: "antigravity"}
	// Compact UI uses the worst bucket so a healthy Gemini group cannot hide Claude exhaustion.
	if fiveHour != nil {
		snapshot.FiveHour = fiveHour
	} else {
		snapshot.FiveHour = daily
	}
	if weekly != nil {
		snapshot.SevenDay = weekly
	} else {
		snapshot.SevenDay = monthly
	}
	if snapshot.FiveHour == nil && snapshot.SevenDay == nil {
		return nil, false
	}
	return snapshot, true
}

func remainingFractionUsedPercent(value any) (float64, bool) {
	var fraction float64
	switch typed := value.(type) {
	case json.Number:
		parsed, errParse := typed.Float64()
		if errParse != nil {
			return 0, false
		}
		fraction = parsed
	case float64:
		fraction = typed
	case string:
		parsed, errParse := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if errParse != nil {
			return 0, false
		}
		fraction = parsed
	default:
		return 0, false
	}
	if math.IsNaN(fraction) || math.IsInf(fraction, 0) {
		return 0, false
	}
	used := (1 - fraction) * 100
	if used < 0 {
		used = 0
	}
	if used > 10_000 {
		used = 10_000
	}
	return used, true
}

func classifyAntigravityWindow(values ...string) antigravityWindowKind {
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		normalized = strings.ReplaceAll(normalized, "_", "-")
		normalized = strings.ReplaceAll(normalized, " ", "-")
		switch {
		case strings.Contains(normalized, "five-hour") || strings.Contains(normalized, "fivehour") || strings.Contains(normalized, "5h"):
			return antigravityWindowFiveHour
		case strings.Contains(normalized, "daily") || normalized == "day":
			return antigravityWindowDaily
		case strings.Contains(normalized, "weekly") || normalized == "week" || strings.Contains(normalized, "-week"):
			return antigravityWindowWeekly
		case strings.Contains(normalized, "monthly") || normalized == "month" || strings.Contains(normalized, "-month"):
			return antigravityWindowMonthly
		}
	}
	return antigravityWindowUnknown
}

func antigravityWindowMinutes(kind antigravityWindowKind) int {
	switch kind {
	case antigravityWindowFiveHour:
		return antigravityFiveHourMinutes
	case antigravityWindowDaily:
		return antigravityDailyMinutes
	case antigravityWindowWeekly:
		return antigravityWeeklyMinutes
	case antigravityWindowMonthly:
		return antigravityMonthlyMinutes
	default:
		return 0
	}
}

func worseUsageWindow(current, candidate *UsageWindowSnapshot) *UsageWindowSnapshot {
	if candidate == nil {
		return cloneUsageWindow(current)
	}
	if current == nil || candidate.UsedPercent > current.UsedPercent {
		return cloneUsageWindow(candidate)
	}
	return cloneUsageWindow(current)
}

func parseAntigravityResetTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed, errParse := time.Parse(time.RFC3339Nano, value); errParse == nil {
		resetAt := parsed.UTC()
		return &resetAt
	}
	if parsed, errParse := time.Parse(time.RFC3339, value); errParse == nil {
		resetAt := parsed.UTC()
		return &resetAt
	}
	dot := strings.IndexByte(value, '.')
	if dot < 0 {
		return nil
	}
	end := dot + 1
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end-(dot+1) <= 9 {
		return nil
	}
	trimmed := value[:dot+1+9] + value[end:]
	if parsed, errParse := time.Parse(time.RFC3339Nano, trimmed); errParse == nil {
		resetAt := parsed.UTC()
		return &resetAt
	}
	return nil
}

func parseAntigravityPlan(raw []byte) string {
	object, ok := decodeQuotaObject(raw)
	if !ok {
		return ""
	}
	for _, key := range []string{"paidTier", "paid_tier", "currentTier", "current_tier"} {
		tier, _ := firstMapValue(object, key).(map[string]any)
		if planType := mapAntigravityTierID(quotaString(firstMapValue(tier, "id", "Id"))); planType != "" {
			return planType
		}
	}
	return ""
}

func mapAntigravityTierID(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "free-tier", "free":
		return "free"
	case "g1-pro-tier", "pro-tier", "pro":
		return "pro"
	case "g1-ultra-tier", "ultra-tier", "ultra":
		return "ultra"
	case "g1-ultra-lite-tier", "ultra-lite-tier", "ultra-lite":
		return "ultra-lite"
	default:
		return safeAccountPlanType(id)
	}
}

func fmtAntigravityUnavailable(err error) error {
	if err == nil {
		return ErrQuotaMetadataUnavailable
	}
	return ErrQuotaMetadataUnavailable
}
