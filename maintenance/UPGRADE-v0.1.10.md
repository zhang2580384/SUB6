# SUB6 0.1.10 installation and upgrade

This release retains formal 0.1.9 and adds the focused
[ordinary WS HTTP bridge history repair](2026-11-17-ws-http-bridge-history.md).
For supported native API-key/Codex OAuth accounts, a completed readable
`store=false` turn can continue on the same logical WS connection with only
`previous_response_id` and new input. Assistant explanation, completed tool calls
and submitted results are retained exactly once. Full client history is not
duplicated, and independent logical conversations on one socket remain isolated.
No downstream software or client setting change is required for this supported
path. Unknown, expired, changed-route, protected or unconfirmed history is still
refused with a structured error; no generation or completed tool is replayed.

On the API-key HTTP bridge, assistant messages with matching provider turn
metadata can continue when the added, done and terminal events agree. Two
numeric fields may be filled in during generation. The final HTTP request
must preserve the completed message. A scoped moderation notice does not become
model input. This extension does not apply to OAuth or to policy failures.

The proof is local to the logical connection and its latest completed response.
It does not survive reconnection or make older IDs, encrypted reasoning,
nonempty annotations or unfamiliar provider extensions portable. Per-session and
process reservation limits apply; this is not a total-memory guarantee. The
maximum five-minute proof lifetime is checked at use, with cleanup at lookup or
session closure. Preserve existing gateway WS sessions until they finish when
performing a separately authorized upgrade. Local fictional providers establish
the regression behavior; real client/provider acceptance is a separate gate.

There are no new SQL migrations, dependencies or settings compared with 0.1.9.
The raw-file migration fingerprint remains
`63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b`.
The existing protocol 2 controller still requires exact release metadata and a
successful read-only preflight. Unknown migrations must remain refused. A
controller or independent whitelist sidecar upgrade remains a separate action.

All [0.1.9 upgrade conditions](UPGRADE-v0.1.9.md) continue to apply, including
temporary pricing-card precedence and the Carbon UTC expiry boundary. Preserve
configuration, keys, runtime resources, PostgreSQL, Redis, backups and all
transactions. A verified retained 0.1.9 application reads the same schema and
cache format, but lacks the ordinary local-history repair. Across older schema
boundaries only the separately validated exact 0.1.5 reader is qualified. Never
restore an old whole database merely to roll back an application. A single-main
administrative switch can briefly reconnect or return 502.

No additional Ranxi range is qualified or integrated. The 2026-11-17 observation
records production `8750ec1956be7c8de327e384c884bb737f943017`, formal v2.21.6
commit `321f11c1a4d23302961c223ac030fd09e3f9e6e8` and tag object
`ac02d5278ced687d920b01ea6eec4227b5b1cf02`; these remain excluded pending
their separate security, migration and rollback gates. The qualified upstream
is still `aa8e596e6d6f3e4648d6beba73b39b240dd5357a` (v2.21.3).
Do not restore rolled-back custom image orchestration, clear ownership records
or automatically enable lossy history/readable-recovery Beta.

Verify all six official assets, exact source commit, embedded version, binary
SHA256, `build-info.json` and `checksums.txt` at
[SUB6 v0.1.10](https://github.com/zhang2580384/SUB6/releases/tag/v0.1.10).
See the [build and review requirements](BUILD-v0.1.10.md). Publication does not
deploy a service; production changes require separate explicit authorization.
