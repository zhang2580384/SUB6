package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// geminiCurrentDefaultPricing supplies the Standard paid USD token rates checked
// at https://ai.google.dev/gemini-api/docs/pricing on 2026-11-15. Re-evaluate on
// each bill, including already-resolved catalog prices, so a running process
// does not retain Carbon's introductory price after the promotion expires.
// Google publishes dates without a timezone; SUB6 explicitly uses UTC here.
// Group/channel cards remain operator-owned and bypass this default policy.
func geminiCurrentDefaultPricing(model string, at time.Time) *ModelPricing {
	model = normalizeGeminiThinkingTierAlias(lastSegment(strings.ToLower(strings.TrimSpace(model))))
	var input, output, read float64
	switch model {
	case "gemini-4-carbon":
		if at.IsZero() {
			at = timezone.Now()
		}
		input, output, read = 2e-6, 10e-6, .1e-6
		if !at.Before(time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)) {
			input, output, read = 4e-6, 20e-6, .2e-6
		}
	case "gemini-4-hydrogen":
		input, output, read = 1.5e-6, 7.5e-6, .075e-6
	default:
		return nil
	}
	// Both models support implicit cache reads only. No cache-write/storage fee
	// or prompt-length surcharge is invented. Priority is 1.8x; Flex is 0.5x.
	return &ModelPricing{
		InputPricePerToken: input, OutputPricePerToken: output, CacheReadPricePerToken: read,
		InputPricePerTokenPriority: input * 1.8, OutputPricePerTokenPriority: output * 1.8,
		CacheReadPricePerTokenPriority: read * 1.8,
	}
}
