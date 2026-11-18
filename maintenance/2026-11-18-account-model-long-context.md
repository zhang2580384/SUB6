# Account-specific long-context pricing

SUB6 base: `55258d8f74bc462d80abc6fc690cd19c513b17d8`. This is a local change to the OpenAI gateway. No upstream range is merged. The upstream pin and migration fingerprint are unchanged.

An administrator can enable per-model long-context billing on an OpenAI API-key account, below the UltraFast promotion. It is disabled by default. While enabled:

| Billing model | Long-context surcharge |
| --- | --- |
| GPT-6.2 Sol | Applies only when its box is checked |
| GPT-6.2 Astra | Applies only when its box is checked |
| GPT-6.1 Sol, GPT-6 Astra | Waived; ordinary token charges still apply |
| Other models | Existing account and group settings |

For the four named models, the account selection takes precedence even when the group enables long-context pricing. It applies at every service tier and is independent of the UltraFast promotion and its deadline. On the bundled GPT-6.2 cards, ordinary input plus cache reads and writes must **exceed** 272,000 tokens: input and cache rates then double and output rates rise to 1.5 times their usual values. When a channel specifies token intervals, a selected model uses its matching interval without a second surcharge. A waived model uses the existing base/first-tier rate. A flat group card can be removed without changing these charges when the matching channel card is identical, including intervals and ancillary prices; this is not a reason to remove the channel catalog.

The gateway evaluates each final billing candidate. The catalog-based account-cost path evaluates the upstream model separately; explicit account-cost rules keep their existing priority. No customer request field can set these switches. The admin flags use existing account metadata and survive partial edits and scheduler projection. No migration, dependency, workflow, payment callback or update-controller change is required. Disable the account's per-model switch to restore its old rules for future requests. Historical ledger entries are not recalculated.

Validation uses synthetic usage events and checks the recorded input, output, cache components and balance charge for all four models at identical context lengths, immediately below/above the threshold, with positive and negative overrides, with both general gates enabled, and with either or both GPT-6.2 boxes cleared. Additional coverage exercises catalog-based procurement, fallback prices, explicit channel intervals, removing a duplicate flat group card, partial updates and scheduler projection. Frontend checks cover account creation, change, inheritance, translations, lint and a production build.
