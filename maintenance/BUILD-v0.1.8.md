# SUB6 0.1.8 distribution build and review

The `distribution` CI job builds the root Dockerfile from `git archive` of the
exact clean commit. It pins the official Docker Library images to the digests
resolved for that build. Go 1.27.4, CGO=0, embedded frontend and BuildType=release
are used. Backend, frontend package, distribution policy and image labels all
use version 0.1.8. Commit/date labels are supplied explicitly; the Docker build
context excludes `.git`, so no embedded Go VCS-clean flag is claimed.

Reproduce in a disposable Linux Docker environment with Python 3.12+:

```sh
git checkout v0.1.8
python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.8-distribution
```

The output directory must not exist and must be outside the checkout. Builds
require network access to official dependency/image registries. Base image
updates can change bytes between builds: the emitted `build-info.json` records
the actual base digests, image ID, exact source tree, binary and image-archive
hashes. Source and runtime archives are normalized to the source commit time.

Six assets are required:

- `sub6-linux-amd64`: extracted from the newly built, never-configured image.
- `sub6_0.1.8_linux_amd64.tar.gz`: the same binary, resources, metadata, licenses,
  runtime instructions and operator-verified-runtime Dockerfile.
- `sub6_0.1.8_source.tar.gz`: complete corresponding source from the exact commit,
  including local BEpusdt/SQLite modules and legal notices.
- `sub6_0.1.8_docker_linux_amd64.tar.gz`: clean `docker save` image archive.
- `build-info.json`: version, provenance, hashes and managed-upgrade contract.
- `checksums.txt`: SHA256 of the other five assets.

After checksum verification, `docker load -i sub6_0.1.8_docker_linux_amd64.tar.gz`
loads `sub6:0.1.8`. This is a downloadable container archive, not a GHCR/Docker Hub
registry publication. It requires independently configured PostgreSQL/Redis,
persistent storage and secrets; see [deployment](../deploy/README.md). The
existing updater consumes the matching runtime/binary assets and does not need
to load the optional image archive.

Build gates run the image without network/config/database, check the full
version/commit banner, pg_dump and psql 18, UID 1000 and writable runtime resource
directories. Packaging verifies runtime resources match committed source.
Publication additionally audits source/blob history, archives and image layers,
then verifies actual uploaded/downloaded asset hashes. Old tags/assets are
immutable. A keyword/secret scan is supporting evidence, not proof that all
code is harmless.

The workflow retains read-only repository permissions, pinned actions and no
production secrets, deployment or automatic Release publishing. The full source,
race, payment, real PostgreSQL/Redis, migration-repeat/rollback-reader and
independent-sidecar jobs must all succeed on the final source commit. Pricing
tests cover both entry points, operator overrides, TTL buckets, cache/thinking
usage and Carbon's expiry. No failing ownership assertion is weakened.

Manual review of this patch covers default-price precedence, money units,
exact SKU matching, amount/multiplier arithmetic, schedule cloning, official
sources, packaging commands, image inputs and CI permissions. It introduces no
new authentication/admin grant, payment callback, dependency, migration,
credential destination, update download host or production action. Wider
candidate risks/exclusions remain described in the upstream review.
