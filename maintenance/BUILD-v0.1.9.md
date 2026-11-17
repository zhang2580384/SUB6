# SUB6 0.1.9 distribution build and review

Use the unchanged [reviewed distribution process](BUILD-v0.1.8.md) with the
exact 0.1.9 source commit. Backend VERSION, frontend package, distribution
policy, image labels and embedded release metadata must all agree on 0.1.9.

```sh
git checkout v0.1.9
python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.9-distribution
```

The six required assets are `sub6-linux-amd64`,
`sub6_0.1.9_linux_amd64.tar.gz`, `sub6_0.1.9_source.tar.gz`,
`sub6_0.1.9_docker_linux-amd64.tar.gz`, `build-info.json` and `checksums.txt`.
Only the installable runtime archive may contain the legacy selector
`linux_amd64`. The optional Docker archive uses `linux-amd64`, retains clean
image configuration and is not a registry publication. Existing release tags
and assets must not be overwritten.

Before publication require complete CI on the final exact source SHA: full
backend/frontend checks and builds, embedded payment tests, real PostgreSQL and
Redis integration, migration repeat/rollback reader, independent sidecar and
clean distribution/image smoke tests. The existing race gate additionally
selects `OpenAIAgentHTTPBridge`, `OpenAIHTTPAndWSBridge` and
`OpenAIWSForwardRecovery` with the original ownership/moderation regressions.
Verify real Redis scenarios ran; skipped Windows fixtures are insufficient.
Keep original failed evidence and ownership/tool assertions intact.

Review both the current tree and every pending reachable commit/blob. Check
source/runtime archives against Git blobs and metadata, review Docker image
configuration and all layers, verify the embedded version/commit and actual
uploaded/downloaded asset hashes. Base image digests and hashes are recorded in
the emitted build-info.json. Secret scans support manual review; they cannot
prove that the source or dependencies are harmless.

The focused code review covers final upstream identity, account/configuration,
encrypted input integrity, provider anchors, current admission, pending/conflict
delivery fences, unchanged cache publication, error framing and slot release.
It introduces no new dependency, migration, payment callback, administrative
grant, credential destination, installer or CI permission. Existing broader
upstream exclusions and the [upgrade conditions](UPGRADE-v0.1.9.md) still apply.
CI remains read-only and never deploys production or publishes a Release.
