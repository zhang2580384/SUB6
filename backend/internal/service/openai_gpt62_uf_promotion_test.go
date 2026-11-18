//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGPT62UFSettledCharge(t *testing.T) {
	end := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	before := end.Add(-time.Nanosecond)
	for _, tc := range []struct {
		name, model, requested, observed string
		sol, astra, trustResponse, ws    bool
		at                               time.Time
		ratio, unit                      float64
	}{
		{"old_account", "gpt-6.2-sol", "ultrafast", "", false, false, false, false, before, 6, 2e-6},
		{"sol_only", "gpt-6.2-sol", "ultrafast", "", true, false, false, true, before, 3, 2e-6},
		{"astra_stays_six", "gpt-6.2-astra", "ultrafast", "", true, false, false, true, before, 6, 8e-6},
		{"astra_only", "openai/gpt-6.2-astra-max", "priority", "ultrafast", false, true, true, false, before, 3, 8e-6},
		{"sol_stays_six", "gpt-6.2-sol", "ultrafast", "", false, true, false, false, before, 6, 2e-6},
		{"both", "gpt-6.2-astra", "ultrafast", "", true, true, false, true, before, 3, 8e-6},
		{"deadline_sol", "gpt-6.2-sol", "ultrafast", "", true, true, false, false, end, 6, 2e-6},
		{"deadline_astra", "gpt-6.2-astra", "ultrafast", "", true, true, false, true, end, 6, 8e-6},
		{"standard_response", "gpt-6.2-astra", "ultrafast", "default", true, true, true, true, before, 1, 8e-6},
		{"fast", "gpt-6.2-sol", "priority", "priority", true, true, true, false, before, 2, 2e-6},
		{"no_upgrade", "gpt-6.2-sol", "", "ultrafast", true, true, false, true, before, 1, 2e-6},
		{"other_model", "gpt-6.1-sol", "ultrafast", "", true, true, false, false, before, 6, 2e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, input, repo, _ := ultrafastUsageHarness(t, ultrafastUsageGroup(), tc.trustResponse)
			if tc.sol {
				input.Account.Extra[OpenAIGPT62SolUFPromotionKey] = true
			}
			if tc.astra {
				input.Account.Extra[OpenAIGPT62AstraUFPromotionKey] = true
			}
			input.PricingAt = tc.at
			input.Result = ultrafastUsageResult(t, tc.model, tc.requested, tc.observed, 1300, 200, 100, 50)
			input.Result.OpenAIWSMode = tc.ws
			require.NoError(t, svc.RecordUsage(t.Context(), input))
			e := ultrafastUsageEvent(t, repo)
			price := tc.unit * tc.ratio
			require.InDelta(t, 1000*price, e.Usage.InputCost, 1e-12)
			require.InDelta(t, 50*5*price, e.Usage.OutputCost, 1e-12)
			require.InDelta(t, 100*1.25*price, e.Usage.CacheCreationCost, 1e-12)
			require.InDelta(t, 200*.05*price, e.Usage.CacheReadCost, 1e-12)
			standard := (1000 + 250 + 125 + 10) * price
			require.InDelta(t, QuantizeUsageBillingAmount(standard*.25), e.Command.BalanceCost, 1e-12)
			require.InDelta(t, QuantizeUsageBillingAmount(standard*.7), e.Command.AccountQuotaCost, 1e-12)
		})
	}
}

func TestGPT62UFPriceCardsAndBoundaries(t *testing.T) {
	at := time.Date(2027, 3, 1, 7, 59, 0, 0, time.UTC)
	acct := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAIGPT62SolUFPromotionKey: true, OpenAIGPT62AstraUFPromotionKey: true}}
	ctx := withAccountUFPromotion(t.Context(), acct, "ultrafast", at)
	for _, svc := range []*BillingService{newTestBillingService(), NewBillingService(nil, newStubPricingServiceFromJSON(t, `{"other":{"input_cost_per_token":1}}`))} {
		for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, svc)} {
			for _, card := range []struct {
				name string
				unit float64
			}{{"gpt-6.2-sol", 2e-6}, {"gpt-6.2-astra", 8e-6}} {
				for _, count := range []int{272000, 272001} {
					enabled := true
					input := CostInput{Ctx: ctx, Model: card.name, Tokens: UsageTokens{InputTokens: count, OutputTokens: 20}, ServiceTier: "ultrafast", PricingAt: at, RateMultiplier: 1, LongContextBillingEnabled: &enabled, Resolver: resolver}
					cost, err := svc.CalculateCostUnified(input)
					require.NoError(t, err)
					inFactor, outFactor := 1.0, 1.0
					if count > 272000 {
						inFactor, outFactor = 2, 1.5
					}
					require.InDelta(t, float64(count)*card.unit*3*inFactor, cost.InputCost, 1e-12)
					require.InDelta(t, 20*card.unit*5*3*outFactor, cost.OutputCost, 1e-12)
					stats := tryModelFilePricing(ctx, svc, card.name, input.Tokens, "ultrafast", at, true)
					require.NotNil(t, stats)
					require.InDelta(t, cost.TotalCost, *stats, 1e-12)
					input.Ctx = t.Context()
					regular, err := svc.CalculateCostUnified(input)
					require.NoError(t, err)
					require.InDelta(t, 2*cost.TotalCost, regular.TotalCost, 1e-12)
				}
				forward := &OpenAIForwardResult{Model: "alias", UpstreamModel: card.name}
				require.True(t, openAISubscriptionModelLongContextExempt(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, forward, UsageTokens{InputTokens: 600000}))
				require.False(t, openAISubscriptionModelLongContextExempt(acct, forward, UsageTokens{InputTokens: 600000}))
			}
		}
	}
}

func TestGPT62UFCustomPrices(t *testing.T) {
	svc := newTestBillingService()
	group := ultrafastUsageGroup()
	group.ModelPricing = []ChannelModelPricing{{Models: []string{"gpt-6.2-astra"}, BillingMode: BillingModeToken, InputPrice: ultrafastUsageFloat(9e-6), CacheWritePrice: ultrafastUsageFloat(0), FastMultiplier: ultrafastUsageFloat(4)}}
	acct := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAIGPT62AstraUFPromotionKey: true}}
	at := time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		tier  string
		ratio float64
	}{{"default", 1}, {"priority", 4}, {"ultrafast", 3}} {
		input := CostInput{Ctx: withAccountUFPromotion(t.Context(), acct, tc.tier, at), Group: group, GroupID: &group.ID, Model: "gpt-6.2-astra", Tokens: UsageTokens{InputTokens: 200, CacheCreationTokens: 100}, RateMultiplier: .5, ServiceTier: tc.tier, PricingAt: at, Resolver: NewModelPricingResolver(nil, svc)}
		cost, err := svc.CalculateCostUnified(input)
		require.NoError(t, err)
		require.Zero(t, cost.CacheCreationCost)
		require.InDelta(t, 200*9e-6*tc.ratio*.5, cost.ActualCost, 1e-12)
	}
}
