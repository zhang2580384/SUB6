package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	OpenAIModelLongContextOverrideKey = "openai_model_long_context_override"
	OpenAIGPT62SolLongContextKey      = "openai_gpt62_sol_long_context"
	OpenAIGPT62AstraLongContextKey    = "openai_gpt62_astra_long_context"
)

type gpt62LongContextSelection struct{ sol, astra bool }
type gpt62LongContextContextKey struct{}

// Capture the selected API credential, not the client's request. A present
// selection with both boxes clear still waives the surcharge on all four cards.
func withAccountGPT62LongContext(ctx context.Context, account *Account) context.Context {
	if account == nil || !account.IsOpenAIApiKey() || account.Extra[OpenAIModelLongContextOverrideKey] != true {
		return ctx
	}
	selection := gpt62LongContextSelection{
		sol:   account.Extra[OpenAIGPT62SolLongContextKey] == true,
		astra: account.Extra[OpenAIGPT62AstraLongContextKey] == true,
	}
	return context.WithValue(ctx, gpt62LongContextContextKey{}, selection)
}

// Resolve per pricing candidate, including when account cost uses the upstream
// model instead of the customer's billing model. All other model cards inherit.
func (input CostInput) longContextPricingEnabled(inherited bool) bool {
	if input.Ctx != nil {
		if selection, ok := input.Ctx.Value(gpt62LongContextContextKey{}).(gpt62LongContextSelection); ok {
			switch openai.GPT62ModelID(input.Model) {
			case "gpt-6.2-sol":
				return selection.sol
			case "gpt-6.2-astra":
				return selection.astra
			}
			if openai.IsGPT61SolModelSpelling(input.Model) || isLongContextGPT6AstraSpelling(input.Model) {
				return false
			}
		}
	}
	return inherited || (input.LongContextBillingEnabled != nil && *input.LongContextBillingEnabled)
}

// Unknown suffixes may have their own operator-provided tariff. Never extend a
// waiver to a new card solely because its name starts with the legacy family.
func isLongContextGPT6AstraSpelling(model string) bool {
	id := openai.CanonicalizeOpenAIModelAliasSpelling(model)
	if id == "gpt-6" || id == "gpt-6-astra" {
		return true
	}
	suffix, ok := strings.CutPrefix(id, "gpt-6-astra-")
	return ok && (suffix == "max" || suffix == "openai-compact" || isKnownCodexModelSuffix(suffix))
}
