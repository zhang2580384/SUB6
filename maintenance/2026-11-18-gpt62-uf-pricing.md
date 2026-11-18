# GPT-6.2 UltraFast rates

The two bundled cards are taken from Wei-Shaw/sub2api `v0.2.23` (`5af9afb588c4b014b3df9db7d4073af1f8ae01b6`). The reviewed model and pricing change was `6107cf17487fe5cb0bd179457ede6b1fe8f7271e`. SUB6 adds independent opt-ins on the selected credential. The Ranxi synchronization base remains in `distribution.json`.

| USD per million tokens | Input | Output | Cache read | Cache write |
| --- | ---: | ---: | ---: | ---: |
| GPT-6.2 Sol | 2 | 10 | 0.10 | 2.50 |
| GPT-6.2 Astra | 8 | 40 | 0.40 | 10 |

On the model token cards Fast is 2×. UltraFast defaults to 6×. Each account may opt either model into 3× for turns started before 2027-03-01 08:00 UTC. From that instant each model returns to 6× even if its saved flag is still true. A downgrade reported by the provider keeps the existing credential tier policy. The promotion affects the final billed tier and pricing model. The standard price can come from the built-in card or an operator-defined token tariff. Existing custom prices, explicit zero cache prices, request-based prices and group/account multipliers retain their meaning. Explicit account-cost rules retain their existing priority. The feature does not grant a provider service tier.

With API long-context pricing enabled, the model cards apply input and cache rates at 2× and output at 1.5× above 272,000 context tokens. At exactly 272,000, ordinary rates apply. As in the reviewed version, OAuth-like subscription credentials are exempt from these two cards' long-context surcharge. The account and group settings and explicit interval tariffs continue to govern their respective paths.

Scope is model pricing for the ordinary OpenAI gateway. Existing independently provisioned regional reservation tariffs have their own price source. No upstream SQL migrations, provider entitlement changes, payment callbacks, dependencies, updater or workflow changes were imported. These changes do not constitute a complete integration of the original project's release. The build uses the existing reviewed distribution process.

Regression coverage includes both account forms, scheduler projection, bundled and fallback cards, an operator tariff, the cutoff and context boundaries, HTTP and WebSocket usage settlement, actual balance and quota commands, and account-statistics pricing. Fixtures use synthetic usage, without production transactions.
