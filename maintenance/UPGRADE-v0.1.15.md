# SUB6 0.1.15

This version applies reviewed changes from Wei-Shaw/sub2api v0.2.24 (`5c2982a81b46e2142d0143d2f83c1ce9c4da1d24`) to the SUB6 v0.1.14 source (`0848e05be354dcaac4d76c68aa603581c79edc73`). The retained Ranxi baseline is v2.21.3.

* Web search gains the optional Exa provider and sources in search results. Operators can see estimated Claude account window costs. The model plaza can copy model IDs, and frequent manual token refreshes show a warning. Claude Code compaction records cache writes with a five-minute lifetime.
* HTTP and WebSocket ingress reject ambiguous fields before routing or charging. Normal clients should send each field once. Model allowlists apply to accepted request encodings. A search-only permission denial ends that request. Timeout, token rotation and model selection fixes improve account availability.
* Gateway parsing, the scheduler cache and tool scanning perform less work. The operations dashboard weights token rate by request duration. Protocol-specific responses cover body read failures, including the effective size limit. Grok search charges follow successful searches and image edits accept five inputs. The protocol bridges preserve tool results, terminal output, context and usage.
* Creating a user from the admin panel commits the user and configured default quotas together. V4 monitoring and accounting delivery retain their existing behavior.

This update introduces no new account-routing option or migration. Existing optional account-routing features keep their v0.1.14 defaults.

The 421 migration files are identical to v0.1.14. The protocol 2 updater must verify the recorded schema fingerprint and drain old connections. A single full instance can briefly reconnect during a switch. Keep the verified previous application package for rollback and retain all transactions and schema. See the [integration review](2026-11-20-weishaw-v024-review.md) for adapted and deferred changes.
