# SUB6 0.1.19 upgrade notes

This candidate selectively adapts reviewed changes from the original Wei-Shaw
Sub2API v0.2.25 release (`348997267d756eab5729bb6b78a275a15d18796a`) on top of
the public SUB6 v0.1.18 baseline. It does not merge Ranxi's production branch.

The changes add authenticated API-key identity variables for account header
overrides, optional synchronization and preview of upstream group display text,
and safer structured error forwarding for OpenAI passthrough requests. Group
synchronization remains opt-in and requires the billing probe to be enabled.

No SQL migration, payment callback, recharge-credit, or customer balance change
is included. The migration fingerprint and controller protocols are unchanged.
This candidate does not update any independent sidecar. Preserve the existing
database and transaction history during upgrades or rollback.

The candidate has not yet completed Linux CI, a formal release, or production
deployment. Do not treat this guide as evidence that installation assets are
available or that a live system has been upgraded.
