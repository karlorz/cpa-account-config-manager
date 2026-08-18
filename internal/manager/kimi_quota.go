package manager

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	kimiQuotaURL        = "https://api.kimi.com/coding/v1/usages"
	kimiFiveHourMinutes = 5 * 60
	kimiDailyMinutes    = 24 * 60
	kimiWeeklyMinutes   = 7 * 24 * 60
	kimiMonthlyMinutes  = 30 * 24 * 60
)

var kimiQuotaNow = time.Now

type kimiWindowKind int

const (
	kimiWindowUnknown kimiWindowKind = iota
	kimiWindowFiveHour
	kimiWindowDaily
	kimiWindowWeekly
	kimiWindowMonthly
)

func isKimiAccount(account Account) bool {
	return strings.EqualFold(strings.TrimSpace(firstNonEmpty(account.Provider, account.Type)), "kimi")
}

func fetchKimiQuotaMetadata(ctx context.Context, client *managementClient, account Account) (quotaMetadata, error) {
	response, errCall := client.APICall(ctx, managementAPICallRequest{
		AuthIndex: account.ID,
		Method:    http.MethodGet,
		URL:       kimiQuotaURL,
		Header: map[string]string{
			"Authorization": "Bearer $TOKEN$",
		},
	})
	if errCall != nil {
		return quotaMetadata{}, ErrQuotaMetadataUnavailable
	}

	status := boundedHTTPStatus(response.StatusCode)
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		snapshot, ok := parseKimiUsagePayload(response.Body)
		if !ok || (snapshot.FiveHour == nil && snapshot.SevenDay == nil) {
			return quotaMetadata{}, ErrQuotaMetadataUnavailable
		}
		return quotaMetadata{quota: snapshot}, nil
	}

	return quotaMetadata{}, quotaMetadataHTTPError{StatusCode: status}
}

func parseKimiUsagePayload(raw []byte) (*QuotaUsageSnapshot, bool) {
	object, ok := decodeQuotaObject(raw)
	if !ok {
		return nil, false
	}

	var fiveHour, daily, weekly, monthly *UsageWindowSnapshot

	// Check top-level "usage" object
	if usageMap, ok := firstMapValue(object, "usage").(map[string]any); ok && usageMap != nil {
		if window, valid := parseKimiRow(usageMap, kimiWindowWeekly); valid {
			weekly = worseUsageWindow(weekly, window)
		}
	}

	// Check "limits" array
	if limits, ok := firstMapValue(object, "limits").([]any); ok && len(limits) > 0 {
		for _, item := range limits {
			limitMap, ok := item.(map[string]any)
			if !ok || limitMap == nil {
				continue
			}

			// Details could be under "detail" sub-map or directly in limitMap
			detailMap, _ := firstMapValue(limitMap, "detail").(map[string]any)
			windowMap, _ := firstMapValue(limitMap, "window").(map[string]any)

			kind, explicitMinutes := classifyKimiWindow(limitMap, windowMap)
			if kind == kimiWindowUnknown {
				continue
			}

			sourceMap := limitMap
			if detailMap != nil {
				sourceMap = detailMap
			}

			window, valid := parseKimiRowWithWindow(sourceMap, limitMap, kind, explicitMinutes)
			if !valid {
				continue
			}

			switch kind {
			case kimiWindowFiveHour:
				fiveHour = worseUsageWindow(fiveHour, window)
			case kimiWindowDaily:
				daily = worseUsageWindow(daily, window)
			case kimiWindowWeekly:
				weekly = worseUsageWindow(weekly, window)
			case kimiWindowMonthly:
				monthly = worseUsageWindow(monthly, window)
			}
		}
	}

	snapshot := &QuotaUsageSnapshot{Provider: "kimi"}
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

func parseKimiRow(data map[string]any, defaultKind kimiWindowKind) (*UsageWindowSnapshot, bool) {
	return parseKimiRowWithWindow(data, nil, defaultKind, 0)
}

func parseKimiRowWithWindow(data map[string]any, fallback map[string]any, kind kimiWindowKind, explicitMinutes int) (*UsageWindowSnapshot, bool) {
	usedPercent, ok := kimiUsedPercent(data)
	if !ok {
		return nil, false
	}

	resetAt := parseKimiResetTime(data)
	if resetAt == nil && fallback != nil {
		resetAt = parseKimiResetTime(fallback)
	}

	minutes := explicitMinutes
	if minutes <= 0 {
		minutes = kimiDefaultWindowMinutes(kind)
	}

	return &UsageWindowSnapshot{
		UsedPercent:   usedPercent,
		ResetAt:       resetAt,
		WindowMinutes: minutes,
	}, true
}

func kimiUsedPercent(data map[string]any) (float64, bool) {
	limitVal, hasLimit := getFloat64(firstMapValue(data, "limit", "max"))
	usedVal, hasUsed := getFloat64(firstMapValue(data, "used", "consumed"))
	remVal, hasRem := getFloat64(firstMapValue(data, "remaining", "remains", "remain"))

	if !hasLimit || limitVal <= 0 {
		return 0, false
	}

	var usedPercent float64
	if hasUsed {
		usedPercent = (usedVal / limitVal) * 100
	} else if hasRem {
		usedPercent = ((limitVal - remVal) / limitVal) * 100
	} else {
		return 0, false
	}

	if math.IsNaN(usedPercent) || math.IsInf(usedPercent, 0) {
		return 0, false
	}

	if usedPercent < 0 {
		usedPercent = 0
	}
	if usedPercent > 10_000 {
		usedPercent = 10_000
	}

	return usedPercent, true
}

func getFloat64(val any) (float64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func classifyKimiWindow(limitMap, windowMap map[string]any) (kimiWindowKind, int) {
	var explicitMinutes int
	if windowMap != nil {
		durationVal, hasDur := getFloat64(firstMapValue(windowMap, "duration", "length", "value"))
		unitStr := strings.TrimPrefix(strings.ToUpper(quotaString(firstMapValue(windowMap, "timeUnit", "time_unit", "unit"))), "TIME_UNIT_")
		if hasDur && durationVal > 0 {
			switch unitStr {
			case "MINUTES", "MINUTE", "MIN", "MINS":
				explicitMinutes = int(durationVal)
			case "HOURS", "HOUR", "H", "HRS":
				explicitMinutes = int(durationVal * 60)
			case "DAYS", "DAY", "D":
				explicitMinutes = int(durationVal * 1440)
			}
		}
	}

	// Classify based on string fields first or explicit minutes / duration
	candidates := []string{
		quotaString(firstMapValue(limitMap, "name")),
		quotaString(firstMapValue(limitMap, "title")),
		quotaString(firstMapValue(limitMap, "scope")),
		quotaString(firstMapValue(limitMap, "window")),
	}

	for _, text := range candidates {
		normalized := strings.ToLower(strings.TrimSpace(text))
		if normalized == "" {
			continue
		}
		normalized = strings.ReplaceAll(normalized, "_", "-")
		normalized = strings.ReplaceAll(normalized, " ", "-")

		if strings.Contains(normalized, "5h") || strings.Contains(normalized, "five-hour") || strings.Contains(normalized, "fivehour") {
			return kimiWindowFiveHour, explicitMinutes
		}
		if strings.Contains(normalized, "daily") || normalized == "day" || strings.Contains(normalized, "-day") {
			return kimiWindowDaily, explicitMinutes
		}
		if strings.Contains(normalized, "week") || strings.Contains(normalized, "weekly") {
			return kimiWindowWeekly, explicitMinutes
		}
		if strings.Contains(normalized, "month") || strings.Contains(normalized, "monthly") {
			return kimiWindowMonthly, explicitMinutes
		}
	}

	// Check explicit duration/minutes
	if explicitMinutes > 0 {
		switch {
		case explicitMinutes <= 360 && explicitMinutes >= 240: // ~5h (300 min)
			return kimiWindowFiveHour, explicitMinutes
		case explicitMinutes == 1440: // 1 day
			return kimiWindowDaily, explicitMinutes
		case explicitMinutes == 10080: // 7 days
			return kimiWindowWeekly, explicitMinutes
		case explicitMinutes >= 40320 && explicitMinutes <= 44640: // ~30 days (43200 min)
			return kimiWindowMonthly, explicitMinutes
		}
	}

	return kimiWindowUnknown, 0
}

func kimiDefaultWindowMinutes(kind kimiWindowKind) int {
	switch kind {
	case kimiWindowFiveHour:
		return kimiFiveHourMinutes
	case kimiWindowDaily:
		return kimiDailyMinutes
	case kimiWindowWeekly:
		return kimiWeeklyMinutes
	case kimiWindowMonthly:
		return kimiMonthlyMinutes
	default:
		return 0
	}
}

func parseKimiResetTime(data map[string]any) *time.Time {
	// Check RFC3339 string fields
	resetStr := quotaString(firstMapValue(data, "reset_at", "resetAt", "reset_time", "resetTime"))
	if resetStr != "" {
		if parsed := parseAntigravityResetTime(resetStr); parsed != nil {
			return parsed
		}
	}

	// Check relative TTL seconds
	ttlVal, ok := getFloat64(firstMapValue(data, "reset_in", "resetIn", "ttl"))
	if ok && ttlVal > 0 {
		resetAt := kimiQuotaNow().UTC().Add(time.Duration(ttlVal) * time.Second)
		return &resetAt
	}

	return nil
}
