# Upgrade to SUB6 0.1.11

This release pairs historical custom tool calls and results across catalog changes and adds account-level GPT-6.2 UltraFast promotional pricing. Refer to the [tool history review](2026-11-18-custom-tool-history.md) and the [pricing review](2026-11-18-gpt62-uf-pricing.md).

Both promotion settings default to off. In **Accounts → Edit → UltraFast 3× promotion**, select Sol, Astra or both on the upstream billing credential. Standard and Fast usage retain their own rates. Review existing custom group and channel token prices before enabling the promotion. The new built-in prices are available even when an external catalog has no entry for these models.

The schema, migration fingerprint and managed updater protocol match 0.1.10. Use the existing controller preflight and drain active streams. Keep a verified 0.1.10 application for rollback. It reads the same database and ignores the two new flags. Rolling back the application also rolls back promotional pricing for subsequent requests. A rollback of the application does not require restoring a database or reversing settled charges. See the [previous upgrade guide](UPGRADE-v0.1.10.md).
