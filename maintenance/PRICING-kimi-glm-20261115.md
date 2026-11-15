# Kimi and GLM token pricing correction

New Kimi model IDs could reach billing without a price, Kimi K2.7 Code could
inherit the older K2 price, and GLM-5.3-FlashX could match the cheaper Flash
substring. Add complete prices to the bundled catalog and builtin fallback,
match the new SKUs before broader family rules, and retain explicit group and
channel price overrides.

This patch is based on SUB6 source commit
`7af3185c72e85298054cc2459dcda2e774afeada`; it does not merge an upstream range
or introduce a database migration, dependency, executable asset or workflow.

## Price basis

Checked on 2026-11-15 against the official international USD list prices:

- [Kimi API pricing](https://platform.kimi.ai/docs/pricing/chat)
- [Z.ai API pricing](https://docs.z.ai/guides/overview/pricing)

Amounts below are USD per million tokens. The application stores USD per token
and applies the existing effective billing multiplier afterward. Domestic CNY
prices and temporary discounts are not used.

| Model | Uncached input | Output | Cached input | Cache write / 5m | Cache write / 1h |
| --- | ---: | ---: | ---: | ---: | ---: |
| kimi-k3.1-pro | 2 | 10 | 0.20 | 2 | 4 |
| kimi-k3.1-pro-highspeed | 4 | 20 | 0.20 | 4 | 8 |
| kimi-k3 | 3 | 15 | 0.30 | 3 | 6 |
| kimi-k2.7-code | 0.95 | 4 | 0.19 | 0.95 | — |
| kimi-k2.7-code-highspeed | 1.90 | 8 | 0.38 | 1.90 | — |
| kimi-k2.6 | 0.95 | 4 | 0.16 | 0.95 | — |
| glm-5.3 | 1.40 | 4.40 | 0.26 | 0 | — |
| glm-5.3-flash | 0.15 | 0.50 | 0.03 | 0 | — |
| glm-5.3-flashx | 0.37 | 1.25 | 0.075 | 0 | — |

K2 cache creation uses the uncached input rate when the upstream usage splits
those tokens into a separate bucket; it is not an additional storage fee.
K3 cache creation honors disjoint 5m and 1h buckets without adding the aggregate
again. Without TTL detail it uses the 5m rate. GLM cache storage is currently free.
K3.1 Pro Highspeed cached reads remain $0.20 rather than doubling with input.

`glm-5.3-prime` has no independent price in the reviewed official tables. An
operator can explicitly put it in the same group price card as `glm-5.3` after
choosing that alias policy. It is not added as an official catalog entry.

## Compatibility and deployment

- Existing group and channel price cards retain precedence over the catalog,
  including explicit zero cache-write prices and models missing a base price.
- Remote catalog entries retain their existing precedence. Bundled fallback
  entries fill missing model IDs; they do not overwrite remote custom prices.
- Recognized provider path prefixes and case differences preserve the price.
  Unknown suffixes for the newly added SKUs are not assigned a guessed price.
- No historical usage, balances or payment behavior is changed. This patch
  does not restore the previously rolled-back image orchestration feature.
- A deployment is needed for new builtin defaults. On an existing version with
  group model price cards, an operator can apply these rates through that
  existing configuration path without restarting the service.

## Validation

Regression coverage exercises both billing entry points with no catalog, an
older catalog, the bundled catalog, and an older catalog merged with bundled
fallback. It checks input, output, cached reads, split cache writes, multiplier
application, namespaced IDs, unknown SKU suffixes, and group cards overriding
missing or stale zero base prices. The Prime case is tested as an explicit
operator alias policy.

Passed:

```sh
go test -tags unit ./internal/service -run 'Test.*(Pricing|CalculateCost|GetFallbackPricing|ComputeCacheCreationCost|Resolve_|GetIntervalPricing)' -count=1
```

The JSON test event log recorded 1,128 passing tests and subtests with no
failures. Strict JSON validation found no duplicate keys, confirmed all nine
official price entries, and confirmed the 218 previously bundled entries were
unchanged. `gofmt` and `git diff --check` passed. This is validation of the source
patch, not a claim of a new binary release or completed production billing.
