# SUB6 0.1.11 distribution

Build the committed tag with the [existing release process](BUILD-v0.1.10.md). The dashboard updater consumes the runtime archive from the six release assets. The version in the backend, frontend, distribution definition, build metadata and runtime must be 0.1.11. Validate the token pricing and history regressions for this revision and compare the uploaded artifacts with their published SHA-256 checksums.

```sh
git checkout v0.1.11
python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.11-distribution
```
