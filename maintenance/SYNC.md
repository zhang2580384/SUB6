# Reproducible upstream synchronization

The public SUB6 history starts from a sanitized root. It deliberately has no
ancestry relationship to the private integration checkout. Do not merge private
refs, push `--all`, `--mirror`, or push inherited tags. The authoritative upstream
snapshot for each public release is recorded by full commit and tree ID in
`upstream.lock.json`; an official release tag alone is insufficient.

## One serialized run

1. Acquire an exclusive local maintenance lock outside public source (for example
   in `.git/sub6-maintenance`). If any merge/rebase or another run is active,
   inspect and report it without starting a competing mutation.
2. Fetch upstream source into a separate local mirror, without executing hooks,
   dependencies, installers or new workflows. Resolve production, main and
   official release tags to exact full SHAs; verify ancestry and record the range.
3. Materialize three separate trees: the previous exact upstream snapshot from
   the lock (`base`), the current clean SUB6 release (`ours`), and the newly
   reviewed upstream snapshot (`theirs`). For a raw-source export use
   `git archive <full-sha>` from the mirror. Confirm the tree ID against the lock.
   Keep all raw exports and their diffs private: removed upstream operations data
   can appear in deletion hunks even if the result tree is clean.
4. Review the upstream range before merge or execution. Follow `AGENTS.md`, with
   explicit authentication/payment/billing/admin, dependency, outbound, plugin,
   migration, installer and CI review. Quarantine concrete suspicious changes.
   Record included and excluded upstream commits, including merge commits.
5. Create a local `codex/` branch from the clean SUB6 release. Apply a three-way
   merge by path using `base`, `ours`, `theirs`: keep ours when theirs equals
   base, take a reviewed/sanitized theirs when ours equals base, otherwise resolve
   deliberately. Handle file additions/deletions/renames explicitly. Never apply
   an unreviewed force replacement or run the raw upstream scripts. This method
   also works without shared Git ancestry. A private integration clone with the
   exact old upstream ancestry may be used instead, followed by a sanitized
   export; never give that ancestry to a public ref.
6. Reapply and verify the feature inventory below. Review the resulting full
   source tree and newly reachable Git history, not just the textual merge diff.
   Remove private domains, credentials, operational evidence, upstream donation
   solicitations, and unreviewed executable assets. Retain licenses, authorship,
   real upstream source citations and required third-party source.
7. Install reviewed locked dependencies with lifecycle hooks disabled. Run the
   focused regressions, frontend lint/typecheck/build/full tests, backend tests
   on Linux, nested-module tests, embedded build, disposable empty/upgrade
   migration and application smoke. Record exact commands and limitations.
8. Record results and upgrade/rollback advice in `maintenance/`, update the lock,
   commit the sanitized result, then audit every commit/blob reachable from the
   intended public refs. A scanner's zero findings is not a security guarantee.
   Publish only explicitly audited SUB6 refs to the authorized repository.
9. Verify the remote exact SHA and CI result; record release/source provenance.
   Do not deploy production. Remove the run lock only after completing or
   explicitly recording the blocked state and preserving private evidence.

## Required SUB6 feature inventory

- SUB6 visible branding, LGPL/upstream attribution, GPL combined-work notices,
  supplied community QR for QQ 1126616545, no upstream donation solicitation.
- Grok/channel-monitor low-token probing: retain `monitorProbe` together with
  native OAuth failure evidence in the Pelican option path; no indiscriminate
  merge of historical billing or rolled-back image orchestration branches.
- Optional direct-access sidecar: authenticated management, ownership and exact
  Origin guards, cumulative positive balance/admin-credit quotas, total overrides,
  audit records, fail-closed edge checks. Its privileged reconciler remains a
  separately reviewed deployment component.
- Configurable HTTP ingress, WS and upstream response read limits; supplied
  source Compose uses 256 MiB, subject to lower endpoint/proxy/model limits.
- Source/manual upgrade policy: no silent upstream binary/image fallback;
  built-in updater accepts this repository's release URLs and mandatory checksums.
- No embedded Google OAuth client credentials; require explicit deployment client
  configuration. Remote credential-service endpoints default empty and fail closed.
- Custom image orchestration remains excluded. Upstream Codex bridge defaults
  false and requires separate capability/resource/production evaluation.
- SMTP and WS deployment topology are configured per installation; never embed
  host tunnel mappings, certificates or live operational configuration.
