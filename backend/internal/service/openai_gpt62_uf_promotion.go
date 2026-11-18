package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	OpenAIGPT62SolUFPromotionKey   = "openai_gpt62_sol_uf_promotion"
	OpenAIGPT62AstraUFPromotionKey = "openai_gpt62_astra_uf_promotion"
)

// Midnight Pacific on March 1, 2027 is 08:00 UTC. A turn keeps its start price
// even when it finishes or its durable charge is replayed after the deadline.
var gpt62UFPromotionEnd = time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)

type gpt62UFPromotion struct{ sol, astra bool }
type gpt62UFPromotionContextKey struct{}

// Carry only the selected credential's flags through the pricing candidates and
// the account-cost calculation. No client header or request field sets them.
func withAccountUFPromotion(ctx context.Context, account *Account, tier string, pricingAt time.Time) context.Context {
	if normalizeBillingServiceTier(tier) != OpenAIFastTierUltrafast || account == nil || !account.IsOpenAI() || pricingAt.IsZero() || !pricingAt.Before(gpt62UFPromotionEnd) {
		return ctx
	}
	promo := gpt62UFPromotion{
		sol:   account.Extra[OpenAIGPT62SolUFPromotionKey] == true,
		astra: account.Extra[OpenAIGPT62AstraUFPromotionKey] == true,
	}
	if !promo.sol && !promo.astra {
		return ctx
	}
	return context.WithValue(ctx, gpt62UFPromotionContextKey{}, promo)
}

func (input CostInput) withGPT62UFPromotion(pricing *ModelPricing) *ModelPricing {
	if normalizeBillingServiceTier(input.ServiceTier) != OpenAIFastTierUltrafast || input.Ctx == nil {
		return pricing
	}
	promo, ok := input.Ctx.Value(gpt62UFPromotionContextKey{}).(gpt62UFPromotion)
	if !ok {
		return pricing
	}
	model := openai.GPT62ModelID(input.Model)
	if !(model == "gpt-6.2-sol" && promo.sol || model == "gpt-6.2-astra" && promo.astra) {
		return pricing
	}
	card := *pricing
	card.ufPromotionalRate = true
	return &card
}
