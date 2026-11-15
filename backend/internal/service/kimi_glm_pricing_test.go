//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Official USD list prices checked on 2026-11-15. Use complete billing paths
// so catalog parsing, TTL buckets, SKU matching and group multipliers are covered.
func TestKimiGLMOfficialPriceBilling(t *testing.T) {
	catalogPath, err := filepath.Abs("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	embedded, err := os.ReadFile(catalogPath)
	require.NoError(t, err)

	for _, source := range []string{"no_catalog", "older_catalog", "embedded_catalog", "older_catalog_with_fallback"} {
		t.Run(source, func(t *testing.T) {
			var catalog *PricingService
			switch source {
			case "older_catalog", "older_catalog_with_fallback":
				catalog = newStubPricingServiceFromJSON(t, `{"kimi-k2.5": {
					"input_cost_per_token": 6e-7, "output_cost_per_token": 3e-6
				}}`)
				if source == "older_catalog_with_fallback" {
					catalog.cfg = &config.Config{}
					catalog.cfg.Pricing.FallbackFile = catalogPath
					catalog.pricingData = catalog.mergeFallbackPricingData(catalog.pricingData)
				}
			case "embedded_catalog":
				catalog = newStubPricingServiceFromJSON(t, string(embedded))
			}
			svc := NewBillingService(&config.Config{}, catalog)
			resolver := NewModelPricingResolver(nil, svc)
			for _, tc := range []struct {
				model               string
				input, output, read float64
				write5m, write1h    float64
			}{
				{"kimi-k3.1-pro", 2, 10, .2, 2, 4},
				{"kimi-k3.1-pro-highspeed", 4, 20, .2, 4, 8},
				{"kimi-k3", 3, 15, .3, 3, 6},
				{"kimi-k2.7-code", .95, 4, .19, .95, 0},
				{"kimi-k2.7-code-highspeed", 1.9, 8, .38, 1.9, 0},
				{"kimi-k2.6", .95, 4, .16, .95, 0},
				{"glm-5.3", 1.4, 4.4, .26, 0, 0},
				{"glm-5.3-flash", .15, .5, .03, 0, 0},
				{"glm-5.3-flashx", .37, 1.25, .075, 0, 0},
			} {
				t.Run(tc.model, func(t *testing.T) {
					tokens := UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000,
						CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000}
					if tc.write1h > 0 {
						tokens.CacheCreationTokens = 2_000_000
						tokens.CacheCreation5mTokens = 1_000_000
						tokens.CacheCreation1hTokens = 1_000_000
					}
					// Case and provider paths must not change a recognized model's bill.
					for _, model := range []string{tc.model, "provider/" + strings.ToUpper(tc.model)} {
						legacy, err := svc.CalculateCost(model, tokens, .35)
						require.NoError(t, err)
						require.InDelta(t, tc.input, legacy.InputCost, 1e-10)
						require.InDelta(t, tc.output, legacy.OutputCost, 1e-10)
						require.InDelta(t, tc.read, legacy.CacheReadCost, 1e-10)
						require.InDelta(t, tc.write5m+tc.write1h, legacy.CacheCreationCost, 1e-10)
						total := tc.input + tc.output + tc.read + tc.write5m + tc.write1h
						require.InDelta(t, total, legacy.TotalCost, 1e-10)
						require.InDelta(t, total*.35, legacy.ActualCost, 1e-10)
						unified, err := svc.CalculateCostUnified(CostInput{
							Ctx: context.Background(), Model: model, Tokens: tokens,
							RateMultiplier: .35, Resolver: resolver,
						})
						require.NoError(t, err)
						legacy.BillingMode = string(BillingModeToken)
						require.Equal(t, legacy, unified)
					}
				})
			}
		})
	}
}

func TestKimiGLMGroupCardsOverrideMissingOrStaleCatalog(t *testing.T) {
	for _, model := range []string{"kimi-k3.1-pro", "glm-5.3", "glm-5.3-prime"} {
		for _, source := range []string{"missing_base_price", "stale_zero_catalog"} {
			t.Run(model+"/"+source, func(t *testing.T) {
				var catalog *PricingService
				if source == "stale_zero_catalog" {
					catalog = newStubPricingServiceFromJSON(t, `{"`+model+`": {
						"input_cost_per_token": 0, "output_cost_per_token": 0,
						"cache_read_input_token_cost": 0, "cache_creation_input_token_cost": 0
					}}`)
				}
				svc := NewBillingService(&config.Config{}, catalog)
				if source == "missing_base_price" {
					// Model not present in either the catalog or the builtin fallback.
					svc.fallbackPrices = map[string]*ModelPricing{}
				}
				in, out, read, write, write1h := 1.4e-6, 4.4e-6, .26e-6, 0.0, 0.0
				if strings.HasPrefix(model, "kimi-") {
					in, out, read, write, write1h = 2e-6, 10e-6, .2e-6, 2e-6, 4e-6
				}
				// Prime is an operator-approved alias of GLM-5.3, not an official SKU quote.
				models := []string{model}
				if strings.HasPrefix(model, "glm-") {
					models = []string{"glm-5.3", "glm-5.3-prime"}
				}
				group := &Group{ID: 123, RateMultiplier: .35, ModelPricing: []ChannelModelPricing{{
					Models: models, BillingMode: BillingModeToken,
					InputPrice: &in, OutputPrice: &out, CacheReadPrice: &read,
					CacheWritePrice: &write,
				}}}
				tokens := UsageTokens{InputTokens: 1000, OutputTokens: 200,
					CacheReadTokens: 3000, CacheCreationTokens: 1000}
				writeCost := 1000 * write
				if write1h > 0 {
					group.ModelPricing[0].CacheWrite1hPrice = &write1h
					tokens.CacheCreation5mTokens, tokens.CacheCreation1hTokens = 600, 400
					writeCost = 600*write + 400*write1h
				}
				resolver := NewModelPricingResolver(nil, svc)
				resolved := resolver.Resolve(context.Background(), PricingInput{
					Model: model, GroupID: &group.ID, Group: group,
				})
				require.Equal(t, PricingSourceGroup, resolved.Source)
				cost, err := svc.CalculateCostUnified(CostInput{
					Ctx: context.Background(), Model: model, Tokens: tokens,
					GroupID: &group.ID, Group: group, RateMultiplier: group.RateMultiplier,
					Resolver: resolver,
				})
				require.NoError(t, err)
				total := 1000*in + 200*out + 3000*read + writeCost
				require.InDelta(t, writeCost, cost.CacheCreationCost, 1e-12)
				require.InDelta(t, total, cost.TotalCost, 1e-12)
				require.InDelta(t, total*.35, cost.ActualCost, 1e-12)
			})
		}
	}
}

func TestKimiGLMUnknownSKUSuffixDoesNotUseCheaperFamilyPrice(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	for _, model := range []string{"kimi-k3.1-pro-unknown", "kimi-k2.7-code-unknown", "kimi-k2.7-code-highspeed-unknown", "glm-5.3-flashx-unknown"} {
		t.Run(model, func(t *testing.T) {
			_, err := svc.CalculateCost(model, UsageTokens{InputTokens: 1000}, 1)
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
		})
	}
}
