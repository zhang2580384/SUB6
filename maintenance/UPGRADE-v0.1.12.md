# Upgrade from 0.1.11 to 0.1.12

The account editor adds independent long-context pricing for GPT-6.2 Sol and Astra. Existing accounts keep their previous pricing until an administrator enables the per-model policy. Select both 6.2 models to apply their long-context rates while GPT-6.1 Sol and GPT-6 Astra keep their ordinary rates. This is independent of the UltraFast tier and promotion. See the [billing policy and tests](2026-11-18-account-model-long-context.md).

The updater protocol and migration fingerprint are unchanged from 0.1.11. Use controller preflight and drain in-flight requests. Reverting the application to 0.1.11 restores the old policy for subsequent requests; existing ledger entries are not re-rated. Do not enable an entire group to approximate a per-account rule.
