# SUB6 0.1.15 distribution

Build the reviewed commit using the [distribution procedure](BUILD-v0.1.12.md) and `python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.15-dist`. Verify that the embedded binary, runtime archive, corresponding source archive and image all identify the release commit recorded in `build-info.json`. The [upgrade notes](UPGRADE-v0.1.15.md) describe compatibility and the migration fingerprint.
