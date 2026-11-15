//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGeminiCarbonHydrogenDefaultBilling(t *testing.T) {
	embedded, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	for _, source := range []string{"missing", "embedded", "stale_zero"} {
		t.Run(source, func(t *testing.T) {
			var catalog *PricingService
			if source == "embedded" {
				catalog = newStubPricingServiceFromJSON(t, string(embedded))
			} else if source == "stale_zero" {
				catalog = newStubPricingServiceFromJSON(t, `{"gemini-4-carbon":{"input_cost_per_token":0,"output_cost_per_token":0},"gemini-4-hydrogen":{"input_cost_per_token":0,"output_cost_per_token":0}}`)
			}
			svc := NewBillingService(&config.Config{}, catalog)
			resolver := NewModelPricingResolver(nil, svc)
			for _, tc := range []struct {
				model               string
				input, output, read float64
			}{
				{"gemini-4-carbon", 2, 10, .1},
				{"gemini-4-hydrogen", 1.5, 7.5, .075},
			} {
				for _, name := range []string{tc.model, "google/" + strings.ToUpper(tc.model) + "-high"} {
					t.Run(name, func(t *testing.T) {
						for _, resolver := range []*ModelPricingResolver{nil, resolver} {
							for _, tier := range []string{"", "priority", "flex"} {
								cost, err := svc.CalculateCostUnified(CostInput{
									Ctx: context.Background(), Model: name, Resolver: resolver,
									PricingAt:      time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC),
									Tokens:         UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000},
									RateMultiplier: .35, ServiceTier: tier,
								})
								require.NoError(t, err)
								mult := 1.0
								if tier == "priority" {
									mult = 1.8
								} else if tier == "flex" {
									mult = .5
								}
								require.InDelta(t, tc.input*mult, cost.InputCost, 1e-10)
								require.InDelta(t, tc.output*mult, cost.OutputCost, 1e-10)
								require.InDelta(t, tc.read*mult, cost.CacheReadCost, 1e-10)
								require.Zero(t, cost.CacheCreationCost)
								require.InDelta(t, (tc.input+tc.output+tc.read)*mult*.35, cost.ActualCost, 1e-10)
							}
						}
						legacy, err := svc.CalculateCost(name, UsageTokens{InputTokens: 1000}, .35)
						require.NoError(t, err)
						expected := geminiCurrentDefaultPricing(name, time.Now()).InputPricePerToken * 1000 * .35
						require.InDelta(t, expected, legacy.ActualCost, 1e-12)
					})
				}
			}
		})
	}
}

func TestGeminiCarbonPromotionBoundaryRepricesResolvedCatalog(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, svc)
	// Deliberately reuse the same pre-resolved price before/after the deadline.
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-4-carbon"})
	for _, tc := range []struct {
		at                  string
		input, output, read float64
	}{
		{"2026-11-30T23:59:59.999999999Z", 2, 10, .1},
		{"2026-12-01T00:00:00Z", 4, 20, .2},
		{"2026-12-01T08:00:00+08:00", 4, 20, .2},
		{"2027-01-01T00:00:00Z", 4, 20, .2},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339Nano, tc.at)
			require.NoError(t, err)
			for _, r := range []*ModelPricingResolver{nil, resolver} {
				cost, err := svc.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gemini-4-carbon",
					Resolver: r, Resolved: resolved, PricingAt: at, RateMultiplier: .5,
					Tokens: UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}})
				require.NoError(t, err)
				require.InDelta(t, tc.input+tc.output+tc.read, cost.TotalCost, 1e-10)
				require.InDelta(t, (tc.input+tc.output+tc.read)*.5, cost.ActualCost, 1e-10)
			}
		})
	}
}

func TestGeminiCarbonHydrogenPreserveOperatorCards(t *testing.T) {
	for _, model := range []string{"gemini-4-carbon", "gemini-4-hydrogen"} {
		for _, source := range []string{PricingSourceGroup, PricingSourceChannel} {
			t.Run(model+"/"+source, func(t *testing.T) {
				svc := NewBillingService(&config.Config{}, nil)
				card := ChannelModelPricing{Models: []string{model}, InputPrice: testPtrFloat64(8e-6),
					OutputPrice: testPtrFloat64(30e-6), CacheReadPrice: testPtrFloat64(.7e-6)}
				resolver := NewModelPricingResolver(nil, svc)
				group := &Group{ID: 100}
				if source == PricingSourceGroup {
					group.ModelPricing = []ChannelModelPricing{card}
				} else {
					card.Platform = PlatformGemini
					repo := &mockChannelRepository{
						listAllFn: func(context.Context) ([]Channel, error) {
							return []Channel{{ID: 1, Name: "fixture", Status: StatusActive, GroupIDs: []int64{100}, ModelPricing: []ChannelModelPricing{card}}}, nil
						},
						getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
							return map[int64]string{100: PlatformGemini}, nil
						},
					}
					resolver = NewModelPricingResolver(NewChannelService(repo, nil, nil, nil, nil), svc)
				}
				resolved := resolver.Resolve(context.Background(), PricingInput{Model: model, GroupID: &group.ID, Group: group})
				require.Equal(t, source, resolved.Source)
				cost, err := svc.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model,
					Resolver: resolver, Resolved: resolved, GroupID: &group.ID, Group: group, RateMultiplier: .35,
					PricingAt: time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC),
					Tokens:    UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 3000}})
				require.NoError(t, err)
				require.InDelta(t, .0161, cost.TotalCost, 1e-12)
				require.InDelta(t, .0161*.35, cost.ActualCost, 1e-12)
			})
		}
	}
}

func TestGeminiCarbonHydrogenNativeUsageSettlesCacheAndThinkingOnce(t *testing.T) {
	for _, model := range []string{"gemini-4-carbon", "gemini-4-hydrogen"} {
		for _, stream := range []bool{false, true} {
			t.Run(model+"/stream="+map[bool]string{true: "true", false: "false"}[stream], func(t *testing.T) {
				body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
				action := "generateContent"
				response := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":800,"candidatesTokenCount":100,"thoughtsTokenCount":50}}`
				contentType := "application/json"
				if stream {
					action = "streamGenerateContent"
					contentType = "text/event-stream"
					response = "data: " + response + "\n\n"
				}
				ctx, recorder := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/"+model+":"+action, body)
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}}}
				account := geminiSignalTestAccount()
				forwarder := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}, tokenProvider: &GeminiTokenProvider{}}
				result, err := forwarder.ForwardNative(context.Background(), ctx, account, model, action, stream, body)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, recorder.Code)
				repo := &openAIRecordUsageLogRepoStub{inserted: true}
				users := &openAIRecordUsageUserRepoStub{}
				billing := newGatewayRecordUsageServiceForTest(repo, users, &openAIRecordUsageSubRepoStub{})
				billing.resolver = NewModelPricingResolver(nil, billing.billingService)
				group := &Group{ID: 77, Platform: account.Platform, Status: StatusActive, Hydrated: true, RateMultiplier: .35}
				require.NoError(t, billing.RecordUsage(context.Background(), &RecordUsageInput{Result: result,
					APIKey: &APIKey{ID: 10, GroupID: &group.ID, Group: group}, User: &User{ID: 20}, Account: account}))
				require.NotNil(t, repo.lastLog)
				require.Equal(t, 200, repo.lastLog.InputTokens)
				require.Equal(t, 800, repo.lastLog.CacheReadTokens)
				require.Equal(t, 150, repo.lastLog.OutputTokens)
				p := geminiCurrentDefaultPricing(model, time.Now())
				total := 200*p.InputPricePerToken + 800*p.CacheReadPricePerToken + 150*p.OutputPricePerToken
				require.Positive(t, total)
				require.InDelta(t, total, repo.lastLog.TotalCost, 1e-12)
				require.InDelta(t, total*.35, users.lastAmount, 1e-12)
			})
		}
	}
}
