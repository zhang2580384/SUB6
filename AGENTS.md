# SUB6 maintenance instructions

## Project scope

SUB6 is an independently maintained distribution based on Sub2API. Preserve the LGPL-3.0 license, copyright notices and accurate attribution to upstream contributors. Public product branding is SUB6. Community QQ group: 1126616545; use the supplied `assets/community/sub6-qq-1126616545.png`. Remove upstream donation QR codes and donation solicitations from this distribution.

## Upstream synchronization

- Track the original author's `https://github.com/Wei-Shaw/sub2api.git`, its `main` branch and official releases. Do not use the Ranxi fork's `production` branch or releases as the default update source. Resolve and record exact commit hashes each run; branch names and release labels alone are insufficient evidence.
- Review changes before merging or running new code, package hooks, installers or CI workflows. Fetching source is allowed. Do not pipe remote scripts into a shell.
- Inspect authentication and authorization, payment callbacks, signature validation, replay protection, amount/currency checks, order ownership, recharge crediting, balance changes, billing and administrative privileges. Also inspect credential handling, outbound destinations, update/install paths, dependencies, lockfiles, plugins, migrations, CI permissions and executable assets.
- Quarantine suspicious changes and document the concrete evidence. Do not infer maliciousness from a keyword alone or claim a scan proves absence of malicious code.
- Merge reviewed changes locally on a `codex/` branch. Resolve conflicts deliberately while retaining SUB6 custom functionality. Use focused regression tests for the affected paths, plus the required builds and checks.
- Record the upstream base, reviewed range, merged or excluded commits, validation, residual concerns and upgrade recommendation in `maintenance/`. Keep production incident details and private evidence outside public source control.
- Serialize maintenance runs. If initial synchronization is still running, a merge/rebase is in progress, or another run owns the workspace, inspect/report progress without starting a competing mutation.

## Publication

- The authorized public repository is `https://github.com/zhang2580384/SUB6`.
- Before pushing any refs or artifacts, audit both the current tree and every commit/blob reachable from those refs. A clean working tree or `.gitignore` is not a history audit.
- Never publish live configuration, credentials, private keys, tokens, proxy credentials, production addresses or domains, customer identities, database exports, request logs, backups or incident payloads. Use clearly fictional examples and placeholders. Preserve necessary legal and upstream source references.
- Inherited private history must remain local unless proven suitable for publication. A sanitized publication history is allowed; document a reproducible upstream synchronization method and retain license/provenance.
- Review GitHub Actions and default permissions before the first push. Public CI must not deploy production, run untrusted PR code with secrets, grant unnecessary write permissions, or silently publish third-party binaries.
- Once review and validation pass, publishing clean SUB6 source to the authorized repository is allowed. Report the exact pushed commit and validation results.

## Production boundary

- This project prepares and validates SUB6. Do not change production services, DNS, firewalls, balances, databases or deployments as part of maintenance.
- Compare against the actual deployed behavior when needed using read-only evidence. Keep private operational evidence outside the repository.
- Previously rolled-back image orchestration must not be re-enabled as an incidental merge result. Identify any upstream equivalent and report its compatibility, resource use and default behavior for separate evaluation.
- After catching up, report migration compatibility, configuration changes, risks, rollback requirements and whether production should upgrade. Production upgrade requires a separate user instruction.
