#!/usr/bin/env python3
"""Build a clean Linux release locally or in CI; never deploy or publish it.

Requires Git, Python 3.12+ and Docker/BuildKit. All inputs are committed source
and official Docker Library base images, resolved to immutable digests first.
Use an output directory outside the checkout which does not already exist.
"""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[1]


def run(*args, capture=False):
    result = subprocess.run(args, cwd=ROOT, check=True,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.STDOUT if capture else None)
    return result.stdout.decode().strip() if capture else None


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def pack_runtime(staging, target, timestamp):
    with target.open('wb') as output, gzip.GzipFile(fileobj=output, mode='wb', mtime=timestamp, filename='') as gz:
        with tarfile.open(fileobj=gz, mode='w|') as tar:
            for path in sorted(staging.rglob('*')):
                assert not path.is_symlink(), path
                name = path.relative_to(staging).as_posix()
                info = tar.gettarinfo(str(path), arcname=name)
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                info.mtime = timestamp
                info.mode = 0o755 if path.is_dir() or name == 'sub6-linux-amd64' else 0o644
                if path.is_file():
                    with path.open('rb') as stream:
                        tar.addfile(info, stream)
                else:
                    tar.addfile(info)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    out = args.output.resolve()
    assert out != ROOT and ROOT not in out.parents, 'output must be outside checkout'
    assert not run('git', 'status', '--porcelain', capture=True), 'commit source before building'
    commit = run('git', 'rev-parse', 'HEAD', capture=True)
    tree = run('git', 'rev-parse', 'HEAD^{tree}', capture=True)
    date = run('git', 'show', '-s', '--format=%cI', 'HEAD', capture=True)
    timestamp = int(run('git', 'show', '-s', '--format=%ct', 'HEAD', capture=True))
    policy = json.loads((ROOT / 'maintenance/distribution.json').read_text())
    version = policy['version']
    assert (ROOT / 'backend/cmd/server/VERSION').read_text().strip() == version
    assert json.loads((ROOT / 'frontend/package.json').read_text())['version'] == version
    assert policy['target'] == 'linux/amd64'
    fingerprint = hashlib.sha256()
    for path in sorted((ROOT / 'backend/migrations').glob('*.sql')):
        fingerprint.update(path.relative_to(ROOT).as_posix().encode() + b'\0' + path.read_bytes() + b'\0')
    assert fingerprint.hexdigest() == policy['migrations_fingerprint'], 'migration policy requires review'
    out.mkdir(parents=True, exist_ok=False)
    image = 'sub6:' + version
    base_images = {}
    for arg, ref in [('NODE_IMAGE', 'node:24-alpine'), ('GOLANG_IMAGE', 'golang:1.27.4-alpine'),
                     ('ALPINE_IMAGE', 'alpine:3.21'), ('POSTGRES_IMAGE', 'postgres:18-alpine')]:
        run('docker', 'pull', '--platform', 'linux/amd64', ref)
        details = json.loads(run('docker', 'image', 'inspect', ref, capture=True))[0]
        immutable = details['RepoDigests'][0]
        assert '@sha256:' in immutable
        base_images[arg] = immutable
    with tempfile.TemporaryDirectory(prefix='sub6-distribution-') as work:
        work = Path(work)
        context = work / 'source'
        context.mkdir()
        archive = work / 'source.tar'
        run('git', 'archive', '--format=tar', '-o', str(archive), commit)
        with tarfile.open(archive) as tar:
            assert all(item.isfile() or item.isdir() for item in tar), 'source symlinks require review'
            tar.extractall(context, filter='data')
        command = ['docker', 'build', '--platform', policy['target'], '--progress=plain',
                   '--tag', image, '--build-arg', 'VERSION=' + version,
                   '--build-arg', 'COMMIT=' + commit, '--build-arg', 'DATE=' + date]
        for key, value in base_images.items():
            command += ['--build-arg', key + '=' + value]
        run(*command, str(context))
        details = json.loads(run('docker', 'image', 'inspect', image, capture=True))[0]
        labels = details['Config']['Labels']
        assert labels['org.opencontainers.image.version'] == version
        assert labels['org.opencontainers.image.revision'] == commit
        assert details['Os'] == 'linux' and details['Architecture'] == 'amd64'
        # No application config, database, exposed ports or network is provided.
        banner = run('docker', 'run', '--rm', '--network', 'none', image, '-version', capture=True)
        assert version in banner and commit in banner, banner
        for program in ('pg_dump', 'psql'):
            banner = run('docker', 'run', '--rm', '--network', 'none', image, program, '--version', capture=True)
            assert '18.' in banner, banner
        run('docker', 'run', '--rm', '--network', 'none', image, 'sh', '-ec',
            'test "$(id -u)" = 1000; test -r /app/resources/model-pricing/model_prices_and_context_window.json; '
            'test -w /app/resources/model-pricing; test -w /app/data')
        container = run('docker', 'create', '--network', 'none', image, capture=True)
        try:
            run('docker', 'cp', container + ':/app/sub2api', str(out / 'sub6-linux-amd64'))
            # Ensure the image's runtime catalog is the exact committed catalog.
            copied = work / 'resources'
            run('docker', 'cp', container + ':/app/resources', str(copied))
            for path in (context / 'backend/resources').rglob('*'):
                if path.is_file():
                    assert digest(path) == digest(copied / path.relative_to(context / 'backend/resources'))
        finally:
            run('docker', 'rm', container)
        raw_image = work / 'image.tar'
        run('docker', 'save', '--output', str(raw_image), image)
        image_name = 'sub6_' + version + '_docker_linux_amd64.tar.gz'
        with raw_image.open('rb') as source, (out / image_name).open('wb') as dest:
            with gzip.GzipFile(fileobj=dest, mode='wb', filename='', mtime=timestamp) as gz:
                shutil.copyfileobj(source, gz)
        source_name = 'sub6_' + version + '_source.tar.gz'
        run('git', '-c', 'tar.umask=0022', 'archive', '--format=tar.gz', '--prefix=sub6-' + version + '/',
            '-o', str(out / source_name), commit)
        binary = out / 'sub6-linux-amd64'
        contract = {key: policy[key] for key in ('controller_protocol', 'deployment_probe_protocol',
                    'migrations_fingerprint', 'online_migration_plan', 'upgrade_guide')}
        contract.update(rolling_compatible=True, installation_mode='managed-reviewed-online')
        metadata = dict(distribution='SUB6', version=version, source_repository='https://github.com/zhang2580384/SUB6',
                        source_commit=commit, source_tree=tree, commit_date=date,
                        upstream_version=policy['upstream_version'], upstream_commit=policy['upstream_commit'],
                        upstream_release_commit=policy['upstream_commit'], toolchain='go1.27.4', target=policy['target'],
                        cgo_enabled=False, embedded_frontend=True, build_type='release',
                        build_context='git archive of exact source_commit; VCS labels supplied explicitly',
                        binary=binary.name, binary_size=binary.stat().st_size, binary_sha256=digest(binary),
                        managed_upgrade=contract, container_image=dict(archive=image_name, archive_sha256=digest(out/image_name),
                        image_id=details['Id'], tag=image, base_images=base_images),
                        runtime_smoke=dict(version=True, postgres_clients=True, nonroot_resource_permissions=True),
                        sidecar_policy='independent service; not replaced by application update')
        (out / 'build-info.json').write_text(json.dumps(metadata, indent=2) + '\n')
        staging = work / 'runtime'
        staging.mkdir()
        for name in ('sub6-linux-amd64', 'build-info.json'):
            shutil.copyfile(out / name, staging / name)
        for name in ('LICENSE', 'THIRD_PARTY_NOTICES.md'):
            shutil.copyfile(context / name, staging / name)
        shutil.copyfile(context / 'deploy/Dockerfile.prebuilt', staging / 'Dockerfile.prebuilt')
        shutil.copytree(context / 'LICENSES', staging / 'LICENSES')
        shutil.copytree(context / 'backend/resources', staging / 'resources')
        (staging / 'README.txt').write_text(
            'SUB6 ' + version + ' Linux amd64 / embedded frontend / BuildType=release\n'
            'Keep resources/, configuration, encryption keys, PostgreSQL and Redis.\n'
            'Verify checksums.txt and read ' + policy['upgrade_guide'] + ' in the matching source.\n'
            'Managed updates require the reviewed protocol 2 controller and a successful preflight.\n'
            'Independent sidecars require separate updates. No production state is included.\n'
            'Application rollback must retain the expanded schema and all transactions; never restore an old whole database.\n'
            'Keep image orchestration and experimental recovery disabled unless separately evaluated.\n'
            'LGPL-3.0 and embedded BEpusdt GPL-3.0 obligations: see LICENSES/ and THIRD_PARTY_NOTICES.md.\n'
            'Corresponding source: https://github.com/zhang2580384/SUB6/tree/' + commit + '\n')
        pack_runtime(staging, out / ('sub6_' + version + '_linux_amd64.tar.gz'), timestamp)
    assets = sorted(path for path in out.iterdir() if path.is_file())
    assert len(assets) == 5
    (out / 'checksums.txt').write_text(''.join(digest(path) + '  ' + path.name + '\n' for path in assets))
    print(json.dumps(metadata, indent=2))


if __name__ == '__main__':
    main()
