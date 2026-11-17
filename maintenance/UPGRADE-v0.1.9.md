# SUB6 0.1.9 installation and upgrade

This release retains the complete formal 0.1.8 source and adds the focused
[HTTP/WS bridge continuation repair](2026-11-17-ws-http-agent-continuation.md)
from [PR #2](https://github.com/zhang2580384/SUB6/pull/2), reviewed at
`95e5623323858fe96c580626d18e33cc6d4dc121`. Formal 0.1.8 does not contain this
repair. Pricing, usage filters/group colors, WS isolation, the protocol 2 updater
and all earlier valid distribution customizations remain included.

For native API-key Responses requests, changing between HTTP ingress and the
WS HTTP bridge can now continue the same proven task when the final upstream
identity and encrypted history are unchanged. Cached ownership records retain
their original format and identity. No Redis clearing or ownership rewrite is
required. Native upstream WS, OAuth, different adapters, changed credentials,
endpoints or models do not become interchangeable. Pending or conflicting
delivery remains a refusal; the repair does not automatically replay tools.

Forwarding-stage recovery refusals send the existing structured error event
before closing the WebSocket. Bounded diagnostic stages help distinguish the
failed check without exposing request content or credentials. Clients control
how they display that event. The regression uses fictional localhost providers;
it does not prove decryption by a real provider or identify a past incident's
exact changed field. Resume the original task and correlate its request ID when
performing separately authorized production acceptance.

Compared with formal 0.1.7 and 0.1.8 there are no new SQL migrations,
dependencies or settings. The exact raw-file migration fingerprint remains
`63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b`.
Use the existing reviewed protocol 2 controller only after read-only preflight
succeeds for the exact published build. Unknown migrations still fail closed.
Controller and independent sidecar updates are separate operator actions.

The [0.1.8 upgrade conditions](UPGRADE-v0.1.8.md) still apply, including review
of temporary group/channel price cards and the Carbon UTC expiry boundary.
Installing this release does not change explicit production price overrides,
balances or historical billing. Preserve configuration, encryption/signing
keys, runtime resources, PostgreSQL, Redis and restorable backups.

Retain and verify the exact previous release before application rollback. For
an existing 0.1.8 database, a retained verified 0.1.8 application has the same
schema and cache format, but again refuses the cross-ingress continuation fixed
here. Across the older schema boundary use only the retained, tested formal
0.1.5 reader and preserve all expanded structures and subsequent transactions.
Never restore an old whole database merely to roll back the application.
The single-main administrative path may briefly reconnect or return 502 during
switching; preserve existing gateway WS connections until they finish.

No additional upstream range is integrated. The later Ranxi production/release
changes remain quarantined pending their separate security, migration and
rollback review. The 2026-11-17 observation records production
`5c48f4b324dd794b1d51d85755d488650ad62825` and formal v2.21.6
`321f11c1a4d23302961c223ac030fd09e3f9e6e8`; neither is integrated or qualified
by this release. Rolled-back custom image orchestration stays excluded; do not
automatically enable an image bridge, lossy history or readable-recovery Beta.

Verify all six official assets, `checksums.txt`, `build-info.json`, exact source
commit, embedded version and binary hash at
[SUB6 v0.1.9](https://github.com/zhang2580384/SUB6/releases/tag/v0.1.9).
See the [build and validation requirements](BUILD-v0.1.9.md).
Publication does not deploy a service. Production upgrades require a separate
explicit operator instruction and successful environment-specific preflight.
