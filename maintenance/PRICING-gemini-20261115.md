# Gemini Carbon and Hydrogen token prices

`gemini-4-carbon` and `gemini-4-hydrogen` could return billable tokens without
a default price. Add exact model entries and builtin defaults using Google's
[Standard paid USD pricing](https://ai.google.dev/gemini-api/docs/pricing),
checked on 2026-11-15. These are token prices, not Batch/Flex discounts.

| Model and period | Uncached input / million | Output including thinking / million | Cached input / million |
| --- | ---: | ---: | ---: |
| Carbon through 2026-11-30 | $2.00 | $10.00 | $0.10 |
| Carbon from 2026-12-01 | $4.00 | $20.00 | $0.20 |
| Hydrogen | $1.50 | $7.50 | $0.075 |

The published dates have no timezone. SUB6 uses an explicit operational boundary
of **2026-12-01 00:00:00 UTC** (08:00 Beijing time). This timezone is a SUB6
policy, not a claim that Google specified UTC. Evaluate the schedule per bill,
including reused resolved prices, so neither a cached catalog nor a running
process keeps Carbon's introductory price forever. The bundled JSON is a
snapshot; the backend default policy owns the date transition. Explicit group
and channel cards retain priority and are not repriced by this policy.
Temporary operator cards also retain priority: a card with Carbon's promotional
rates will not expire merely because this release is installed. Review and
explicitly remove or migrate it before the date change if scheduled defaults
are desired; keep existing billing multipliers. No install-time configuration
rewrite or historical rebilling is performed.

Both models support implicit cache reads only; no explicit-cache creation or
storage fee is introduced. Priority token prices are explicitly 1.8x Standard;
the existing Flex multiplier is 0.5x. Provider path prefixes, case differences
and the existing exact high/medium/low/tiered aliases retain the base price.
Unknown suffixes are not added as official SKUs.

Regressions cover missing, bundled and stale zero catalogs; legacy and unified
billing; promotion boundary instants and equivalent timezone offsets; a reused
resolved price across the deadline; group/channel overrides and billing
multipliers. Native buffered and streaming fixtures run forwarding followed by
the real usage-settlement entry: prompt 1,000 including 800 cached tokens means
200 uncached input, 800 cached input, and 100 generated + 50 thinking output.
The cached portion is charged once. No real upstream paid request or historical
rebilling is implied by these fixtures.

This corrects pricing defaults only. Provider account availability, upstream
503/502/429 responses, and zero-token failed requests require separate diagnosis.
No authentication, payment callback, balance operation, dependency or migration
is added. Existing Argon/Flash promotional schedules are outside this patch.
