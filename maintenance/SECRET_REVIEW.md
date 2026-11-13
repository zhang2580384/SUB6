# Publication secret review

Use a private report directory outside public source control. Scan the complete
exported tree, then every commit reachable from the exact refs intended for
publication (for example `gitleaks git --log-opts="--all"` in a dedicated
publication repository containing ONLY those refs). Do not treat `.gitignore`,
a clean worktree, or scanning the last diff as a history audit.

The initial source review classified Gitleaks 8.30.1 findings individually:
synthetic test keys, deliberately invalid/truncated PEM/JWT redaction fixtures,
an Ed25519
publisher PUBLIC key, protocol literals, SQL identifiers, dependency hashes and
documented placeholders. No deployed password/token/key was intentionally
accepted. Inherited Google installed-app OAuth constants were initially reviewed
as public application identifiers, then removed after GitHub push protection
rejected the first unpublished root. The release requires explicit OAuth client
configuration and does not bypass GitHub's protection.

`secret-scan-allowlist.json` stores only exact file/rule/value SHA256 digests and
review reasons. It contains no secret values and does not ignore directories or
whole detector rules. Run the scanner into a private UNREDACTED JSON report,
then `python maintenance/check-secret-report.py /private/report.json`. A changed
value, added finding or unmatched path fails review. Redact reports before
sharing; never commit the raw report. A fully redacted report cannot be matched
and is rejected by this comparison.

Also manually inspect binaries, tracked configs, executable assets, operational
records, private domain/IP/customer identifiers, CI permissions and inherited
history. Keyword and entropy detectors have false negatives. Detector results
are evidence for review, never proof of absence of secrets or malicious code.
