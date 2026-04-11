package service

import "strings"

type usageCostCalculator func(model string) (*CostBreakdown, error)

func optionalTrimmedStringPtr(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// optionalNonEqualStringPtr returns a pointer to value if it is non-empty and
// differs from compare; otherwise nil. Used to store upstream_model only when
// it differs from the requested model.
func optionalNonEqualStringPtr(value, compare string) *string {
	if value == "" || value == compare {
		return nil
	}
	return &value
}

func forwardResultBillingModel(requestedModel, upstreamModel string) string {
	if trimmed := strings.TrimSpace(requestedModel); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(upstreamModel)
}

func isPricingLookupMiss(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "pricing not found") || strings.Contains(msg, "no pricing available")
}

func calculateUsageCostWithUpstreamFallback(primaryModel, upstreamModel string, calculate usageCostCalculator) (*CostBreakdown, string, error) {
	primary := strings.TrimSpace(primaryModel)
	fallback := strings.TrimSpace(upstreamModel)
	if primary == "" {
		primary = fallback
	}

	cost, err := calculate(primary)
	if err == nil || fallback == "" || strings.EqualFold(primary, fallback) || !isPricingLookupMiss(err) {
		return cost, primary, err
	}

	fallbackCost, fallbackErr := calculate(fallback)
	if fallbackErr == nil {
		return fallbackCost, fallback, nil
	}

	return cost, primary, err
}

func optionalInt64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
