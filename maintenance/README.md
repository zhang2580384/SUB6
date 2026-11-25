# SUB6 upstream maintenance

SUB6 maintains its own branding, release process and custom gateway behavior while selectively integrating reviewed changes from the original [Wei-Shaw Sub2API](https://github.com/Wei-Shaw/sub2api). The Ranxi fork is not the default synchronization source.

This directory records public-safe synchronization and validation results. Reports must identify exact upstream and SUB6 commit hashes, the reviewed range, security-sensitive changes, included or excluded commits, regression/build results and remaining concerns. Do not include production settings, customer information or raw operational logs.

Upstream changes must be inspected before merge or execution. Payment/recharge/balance behavior, authentication, secret handling, update/install paths, dependencies and CI permissions require explicit review. Passing automated scans is supporting evidence, not proof that code is free of malicious behavior.

Initial publication must audit the whole reachable Git history and release artifacts. Keep inherited private history local when it cannot be safely published. Preserve the LGPL license, copyright notices and upstream attribution.

Production deployment is a separate operation. A completed synchronization should produce an upgrade recommendation and compatibility notes; it must not automatically deploy to a live service.
