//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGPT62AccountLongContextSettlement(t *testing.T) {
	both := &gpt62LongContextSelection{true, true}
	sol := &gpt62LongContextSelection{sol: true}
	astra := &gpt62LongContextSelection{astra: true}
	for _, tc := range []struct {
		name, model, tier      string
		selection              *gpt62LongContextSelection
		total                  int
		group, promo, long     bool
		unit, cacheRatio, rate float64
	}{
		{"sol_boundary", "gpt-6.2-sol", "ultrafast", sol, 272000, false, true, false, 2e-6, .05, 3},
		{"sol_plus_one", "gpt-6.2-sol", "ultrafast", sol, 272001, false, true, true, 2e-6, .05, 3},
		{"astra_standard", "gpt-6.2-astra", "default", astra, 272001, false, false, true, 8e-6, .05, 1},
		{"astra_only", "gpt-6.2-sol", "ultrafast", astra, 272001, true, false, false, 2e-6, .05, 6},
		{"sol_only", "gpt-6.2-astra", "ultrafast", sol, 272001, true, true, false, 8e-6, .05, 3},
		{"neither", "gpt-6.2-astra", "ultrafast", &gpt62LongContextSelection{}, 272001, true, true, false, 8e-6, .05, 3},
		{"no_override", "gpt-6.2-astra", "ultrafast", nil, 272001, true, false, true, 8e-6, .05, 6},
		{"selected_sol", "gpt-6.2-sol", "ultrafast", both, 500001, true, true, true, 2e-6, .05, 3},
		{"selected_astra", "gpt-6.2-astra", "ultrafast", both, 500001, true, true, true, 8e-6, .05, 3},
		{"legacy_sol", "gpt-6.1-sol", "ultrafast", both, 500001, true, true, false, 2e-6, .05, 6},
		{"legacy_astra", "gpt-6-astra", "ultrafast", both, 500001, true, true, false, 10e-6, .1, 6},
		{"legacy_inherits_without_override", "gpt-6.1-sol", "ultrafast", nil, 500001, true, true, true, 2e-6, .05, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := ultrafastUsageGroup()
			group.LongContextPricingEnabled = tc.group
			svc, input, repo, _ := ultrafastUsageHarness(t, group, true)
			input.Account.Extra[openAILongContextBillingEnabledKey] = tc.group
			if tc.selection != nil {
				input.Account.Extra[OpenAIModelLongContextOverrideKey] = true
				input.Account.Extra[OpenAIGPT62SolLongContextKey] = tc.selection.sol
				input.Account.Extra[OpenAIGPT62AstraLongContextKey] = tc.selection.astra
			}
			input.Account.Extra[OpenAIGPT62SolUFPromotionKey] = tc.promo
			input.Account.Extra[OpenAIGPT62AstraUFPromotionKey] = tc.promo
			input.Result = ultrafastUsageResult(t, tc.model, tc.tier, tc.tier, tc.total, tc.total-2000, 1000, 500)
			require.NoError(t, svc.RecordUsage(t.Context(), input))
			e := ultrafastUsageEvent(t, repo)
			in, out := tc.unit*tc.rate, tc.unit*5*tc.rate
			if tc.long {
				in *= 2
				out *= 1.5
			}
			require.Equal(t, tc.long, e.Usage.LongContextBillingApplied)
			require.InDelta(t, 1000*in, e.Usage.InputCost, 1e-10)
			require.InDelta(t, float64(tc.total-2000)*in*tc.cacheRatio, e.Usage.CacheReadCost, 1e-10)
			require.InDelta(t, 1000*1.25*in, e.Usage.CacheCreationCost, 1e-10)
			require.InDelta(t, 500*out, e.Usage.OutputCost, 1e-10)
			want := 1000*in + float64(tc.total-2000)*in*tc.cacheRatio + 1000*1.25*in + 500*out
			require.InDelta(t, QuantizeUsageBillingAmount(want*.25), e.Command.BalanceCost, 1e-10)
		})
	}
}

func TestGPT62AccountLongContextProcurementAndInterval(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAIModelLongContextOverrideKey: true, OpenAIGPT62SolLongContextKey: true}}
	ctx := withAccountGPT62LongContext(t.Context(), account)
	billing := newTestBillingService()
	tokens := UsageTokens{InputTokens: 300000}
	at := time.Date(2027, 3, 2, 0, 0, 0, 0, time.UTC) // long-context selection has no promotion deadline
	for _, tc := range []struct {
		model string
		want  float64
	}{{"gpt-6.2-sol", 1.2}, {"openai/gpt-6.2-sol-xhigh", 1.2}, {"gpt-6.2-astra", 2.4}, {"gpt-6.1-sol", .6}, {"openai/gpt-6", 3}} {
		cost := tryModelFilePricing(ctx, billing, tc.model, tokens, "default", at, true)
		require.NotNil(t, cost)
		require.InDelta(t, tc.want, *cost, 1e-10, tc.model)
		// Same decision without a pricing resolver, whose default gate is enabled.
		fallback, err := billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: tc.model, Tokens: tokens, RateMultiplier: 1, PricingAt: at})
		require.NoError(t, err)
		require.InDelta(t, tc.want, fallback.TotalCost, 1e-10, tc.model)
	}
	resolved := &ResolvedPricing{BasePricing: &ModelPricing{InputPricePerToken: 2e-6}, longContextPricingEnabled: true,
		Intervals: []PricingInterval{{MinTokens: 272000, InputMultiplier: pricingMultiplier(3)}}}
	for _, tc := range []struct {
		model string
		want  float64
	}{
		{"gpt-6.2-sol", 1.8}, {"gpt-6.2-astra", .6}, {"gpt-6.1-sol", .6}, {"gpt-6-astra", .6}, {"gpt-6.1-terra", 1.8},
		{"openai/gpt-6-astra-2026-09-01", .6}, {"gpt-6-astra-example-other", 1.8},
	} {
		custom, err := billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: tc.model, Tokens: tokens, RateMultiplier: 1,
			Resolver: NewModelPricingResolver(nil, billing), Resolved: resolved})
		require.NoError(t, err)
		require.InDelta(t, tc.want, custom.TotalCost, 1e-10, tc.model)
	}
	// Stored boxes have no effect on OAuth or when the master switch is disabled.
	for _, disabled := range []Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: account.Extra},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAIModelLongContextOverrideKey: false, OpenAIGPT62SolLongContextKey: true}},
	} {
		baseline := CostInput{Ctx: withAccountGPT62LongContext(t.Context(), &disabled), Model: "gpt-6.2-sol"}
		require.False(t, baseline.longContextPricingEnabled(false))
		baseline.Model = "gpt-6.1-sol"
		require.True(t, baseline.longContextPricingEnabled(true))
	}
}

func TestGPT62FlatGroupCardRemovalKeepsChannelCharge(t *testing.T) {
	const gid = int64(100)
	cards := []ChannelModelPricing{
		{Platform: PlatformOpenAI, Models: []string{"gpt-6.2-sol"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(3e-6), CacheWritePrice: testPtrFloat64(0)},
		{Platform: PlatformOpenAI, Models: []string{"gpt-6.2-astra"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(9e-6), CacheWritePrice: testPtrFloat64(0)},
	}
	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) {
			return []Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{gid}, ModelPricing: cards}}, nil
		},
		getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
			return map[int64]string{gid: PlatformOpenAI}, nil
		},
	}
	billing := newTestBillingService()
	resolver := NewModelPricingResolver(NewChannelService(repo, nil, nil, nil, nil), billing)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
		OpenAIModelLongContextOverrideKey: true, OpenAIGPT62SolLongContextKey: true, OpenAIGPT62AstraLongContextKey: true,
		OpenAIGPT62SolUFPromotionKey: true, OpenAIGPT62AstraUFPromotionKey: true,
	}}
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ctx := withAccountGPT62LongContext(withAccountUFPromotion(t.Context(), account, "ultrafast", at), account)
	for _, card := range cards {
		group := &Group{ID: gid, Platform: PlatformOpenAI, ModelPricing: cards}
		input := CostInput{Ctx: ctx, Group: group, GroupID: &group.ID, Model: card.Models[0], Resolver: resolver, PricingAt: at,
			Tokens: UsageTokens{InputTokens: 300000, OutputTokens: 75, CacheReadTokens: 1000, CacheCreationTokens: 50}, RateMultiplier: .25, ServiceTier: "ultrafast"}
		quote := func(source string) *CostBreakdown {
			t.Helper()
			input.Resolved = resolver.Resolve(ctx, PricingInput{Model: input.Model, GroupID: input.GroupID, Group: group})
			require.Equal(t, source, input.Resolved.Source)
			cost, err := billing.CalculateCostUnified(input)
			require.NoError(t, err)
			require.InDelta(t, 300000**card.InputPrice*2*3, cost.InputCost, 1e-10)
			require.Zero(t, cost.CacheCreationCost)
			return cost
		}
		before := quote(PricingSourceGroup)
		group.ModelPricing = nil
		require.Equal(t, before, quote(PricingSourceChannel))
	}
}

func TestGPT62AccountLongContextPartialUpdate(t *testing.T) {
	acct := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{OpenAIModelLongContextOverrideKey: true, OpenAIGPT62SolLongContextKey: true, OpenAIGPT62AstraLongContextKey: true}}
	extra, err := normalizeOpenAILongContextBillingUpdateExtra(acct, &UpdateAccountInput{Extra: map[string]any{OpenAIGPT62SolLongContextKey: false}})
	require.NoError(t, err)
	require.Equal(t, false, extra[OpenAIGPT62SolLongContextKey])
	require.Equal(t, true, extra[OpenAIGPT62AstraLongContextKey])
	require.Equal(t, true, extra[OpenAIModelLongContextOverrideKey])
	_, err = normalizeOpenAILongContextBillingUpdateExtra(acct, &UpdateAccountInput{Extra: map[string]any{OpenAIGPT62SolLongContextKey: "true"}})
	require.Error(t, err)
}
