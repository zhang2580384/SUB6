"""Validate a private gitleaks JSON report against exact reviewed false positives.

Run gitleaks without redaction into a PRIVATE report, never commit that report.
Then: python maintenance/check-secret-report.py /private/report.json
Scanning output that was redacted cannot be matched and fails closed.
"""
import hashlib
import json
from pathlib import Path
import sys

allow = json.loads(Path(__file__).with_name('secret-scan-allowlist.json').read_text(encoding='utf-8'))
keys = {(e['file'], e['rule'], e['value_digest']) for e in allow['entries']}
report = json.loads(Path(sys.argv[1]).read_text(encoding='utf-8-sig'))
unreviewed = []
for hit in report:
    path = hit['File'].replace('\\', '/')
    if 'publication-tree/' in path:
        path = path.split('publication-tree/', 1)[1]
    digest = hashlib.sha256(hit['Secret'].encode()).hexdigest()
    if (path, hit['RuleID'], digest) not in keys:
        unreviewed.append((path, hit['RuleID'], hit['StartLine']))
for path, rule, line in unreviewed:
    print(f'REVIEW REQUIRED {path}:{line} [{rule}]')
print(f'{len(report)} findings checked; {len(unreviewed)} require review')
sys.exit(bool(unreviewed))
