# SUB6 0.1.8 installation and upgrade

This release combines the reviewed Ranxi v2.21.3 candidate with the stable 0.1.7
updater repair and the Kimi, GLM and Gemini pricing corrections. Stable 0.1.5 WS
isolation, 0.1.6 usage filters/group colors and pool-close fixes remain included.
See the [upstream review](2026-11-15-upstream-review.md),
[stable merge](2026-11-15-v0.1.8-stable-sync.md),
[Kimi/GLM prices](PRICING-kimi-glm-20261115.md) and
[Gemini prices and date policy](PRICING-gemini-20261115.md).

Compared with **formal 0.1.7**, this release adds no SQL migration or dependency.
The migration fingerprint remains
`63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b`.
Do not interpret a source version as the actual deployed database state.

The existing reviewed protocol 2 controller can install this release after its
read-only preflight succeeds. From exact stable 0.1.5, only the reviewed
`usage-platform-v1` additive plan is allowed: nullable usage-platform snapshot
and concurrent index. Schema/ledger checks, timeout, capacity checks, readiness,
routing and stream draining remain mandatory. Unknown migrations still fail
closed. See [online upgrade and rollback](2026-11-15-v0.1.7-online-upgrade.md).
Installing/upgrading the controller is a separate operator action.

Before upgrading, review temporary **group and channel price cards**. They take
precedence over the new defaults, so installing 0.1.8 alone does not make a
temporary Carbon promotional card double on December 1. The operator must
explicitly decide whether to retain a custom sale price or remove/migrate that
card to the scheduled default, while retaining the group's existing multiplier.
Do not change cards automatically during installation. Review the documented
UTC boundary and set a follow-up before the promotion expires.

Keep configuration, encryption/signing keys, working-directory resources,
PostgreSQL and Redis. Backups must be restorable. An independent IP-allowlist
sidecar requires separate maintenance; replacing the main application does not
upgrade it. Google OAuth clients remain operator-provided. No production
configuration or credentials are included in the release.

The API gateway preserves existing WS connections while new requests move to
the ready version. The single-main administrative path may briefly reconnect
or return 502 during switching; uninterrupted operation is not guaranteed.
Retain and verify the previous exact release before application rollback.
Across the old schema boundary, only the retained, tested formal 0.1.5 is
approved: keep all new structures, migration records and subsequent
transactions. Never restore an old whole database to roll back an application.

Rolled-back custom image orchestration remains excluded. Do not automatically
enable the upstream image bridge, lossy history or readable-recovery Beta.
The 215 commits observed after reviewed v2.21.3 through v2.21.4
`0e8e97aa23afd8e88a61ea64a84a8fb84f1bfdc6` remain excluded pending a full review;
the previously deferred 16 BPS commits are within that range, not additional.

Only official assets at
[SUB6 Releases](https://github.com/zhang2580384/SUB6/releases/tag/v0.1.8)
constitute this release. Verify `checksums.txt`, `build-info.json`, exact source
commit and binary hash. A formal release is distinct from deployment. CI never
connects to production. Production upgrades require explicit operator approval.
