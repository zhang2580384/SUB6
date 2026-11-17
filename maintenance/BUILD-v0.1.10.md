# SUB6 0.1.10 distribution build and review

Use the unchanged [reviewed distribution process](BUILD-v0.1.8.md) at the exact
0.1.10 source commit. Backend VERSION, frontend package, distribution policy,
image labels and embedded metadata must agree.

```sh
git checkout v0.1.10
python3 maintenance/package-distribution.py --output /tmp/sub6-0.1.10-distribution
```

Required assets: `sub6-linux-amd64`, `sub6_0.1.10_linux_amd64.tar.gz`,
`sub6_0.1.10_source.tar.gz`, `sub6_0.1.10_docker_linux-amd64.tar.gz`,
`build-info.json` and `checksums.txt`. Only the runtime archive contains the
legacy selector `linux_amd64`. Docker is distributed as a clean save archive;
there is no registry push. Existing tags and assets must not be overwritten.

Apply all [0.1.9 validation requirements](BUILD-v0.1.9.md) to the final exact
0.1.10 SHA. The ownership/recovery race selection additionally includes
`OpenAIWSHTTPBridgeHistory`, with original ownership and tool assertions intact.
Require all four CI jobs, including real Redis scenarios and race tests;
skipped Windows Redis fixtures are insufficient. Record earlier failures.

Review the [ordinary bridge history repair](2026-11-17-ws-http-bridge-history.md),
especially local scope, current admission, immutable final-send identity/body,
actual downstream delivery, output/delta completeness, tool-result preservation,
memory reservations and refusal of uncertain replay. No new payment callback,
administrative grant, credential destination, installer, dependency or CI
permission is introduced. Workflows retain read-only permissions and do not
publish releases or deploy services.

Audit the current tree and every reachable pending commit/blob before pushing.
Review source/runtime archives against exact Git blobs, image configuration and
all layers, embedded version/commit and all six uploaded/publicly downloaded
asset hashes. Preserve LGPL attribution and licenses. Secret scans are supporting
evidence, not proof of harmlessness. Publication and deployment remain separate.
