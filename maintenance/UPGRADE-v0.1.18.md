# SUB6 0.1.18

This release reduces repeated work on large OpenAI Responses requests. The gateway reads an OAuth input array and Codex account metadata directly from the request during normalization. It allocates a new output list only when it removes an unpaired tool result. Paired tools, item references and named standalone results retain their existing order and content.

The WebSocket handler reuses its client metadata check across thread and execution lane lookups. SUB6 still uses the authenticated connection’s bound logical identity and transport stream when deriving execution scope. Frames that request Responses Lite pass full JSON validation before that mode can take effect. The tool schema precheck skips unnecessary scans while preserving the existing lookaround and object-constraint rules.

The selected changes come from Ranxi PRs #1013, #1014, #1015, #1016, #1019 and #1020. Exact upstream commits are listed in the [upstream lock](upstream.lock.json). The public base is SUB6 0.1.17. The 421 SQL migrations, dependency manifests, CI workflow, account-model policy, billing delivery and managed updater are identical to that release. The remaining upstream range, including migration 393, is outside this release.

For public installations, verify the archive against checksums.txt and run the normal protocol 2 preflight. A site with custom migrations requires a package from the matching source line. For application rollback, keep the verified previous package and preserve the schema and all transactions. A single full instance may briefly reconnect during a switch.
