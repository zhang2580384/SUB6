# SUB6 0.1.18 distribution

Use the [distribution procedure](BUILD-v0.1.12.md) on the release commit. Run `python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.18-dist` in a clean checkout. The binary, runtime archive, source archive and image identify the commit in `build-info.json`. Check each file against `checksums.txt`. See the [upgrade guide](UPGRADE-v0.1.18.md) for compatibility.
