#!/usr/bin/env python3
"""Operator-installed Unix-socket controller for reviewed SUB6 Docker releases.

The web application receives neither a Docker socket nor a general command API.
All deployment paths and container templates are operator-owned configuration.
"""
import argparse
import copy
import datetime
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import shutil
import socketserver
import stat
import subprocess
import tarfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = 'zhang2580384/SUB6'
VERSION = re.compile(r'^[0-9]+\.[0-9]+\.[0-9]+$')
SHA256 = re.compile(r'^[0-9a-f]{64}$')
TERMINAL = {'idle', 'completed', 'failed'}
MODEL_PATHS = '/v1/* /v1beta/* /openai/* /anthropic/* /responses /responses/* /messages /messages/* /chat/* /backend-api/codex/* /api/v3/contents/* /v3/contents/* /contents/* /models /usage'
PLATFORM_BASE = 'a6990935dd92d8132f2d487ba2b3320ac10d8819e72363db2d240bca23e4063f'
PLATFORM_SCHEMA = '63ce16fa855d9f331372aada34df859275777becb181535372c8abec25cf481b'
PLATFORM_PLAN = 'usage-platform-v1'
PLATFORM_MIGRATIONS = {
    '343_usage_log_platform_snapshot.sql': 'c2746f945d71ad861140220a05df2a68a5a014a8217cb3cb8e8975a05876fbc2',
    '344_usage_log_platform_index_notx.sql': '2b6aab65b6030af5de39c2ccce0c833cc16c0e9500b41a2bc79d8893814f5711',
}


def release_checksum(text, name):
    found = [parts[0] for line in text.splitlines()
             if len(parts := line.split()) == 2 and parts[1].lstrip('*') == name]
    if len(found) != 1 or not SHA256.fullmatch(found[0]):
        raise ValueError('Missing, invalid or duplicate mandatory release checksum')
    return found[0]


def atomic_json(path, value):
    path = Path(path)
    temp = path.with_name(path.name + '.tmp')
    with temp.open('w', encoding='utf-8') as stream:
        os.chmod(temp, 0o600)
        json.dump(value, stream, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    temp.replace(path)


def checked_version(value):
    if not isinstance(value, str) or not VERSION.fullmatch(value):
        raise ValueError('A stable SUB6 version is required')
    return value


def file_sha256(path):
    with open(path, 'rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def copy_runtime_config(source, destination):
    """App-owned data must never make a privileged copy follow host symlinks."""
    source, destination = Path(source), Path(destination)
    if source.resolve() != source:
        raise ValueError('Application data directory must not be a symlink')
    destination.mkdir(mode=0o700)
    directory = os.open(source, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for name in ('config.yaml', '.installed', 'model_pricing.json', 'model_pricing.sha256'):
            try:
                fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=directory)
            except FileNotFoundError:
                continue
            with os.fdopen(fd, 'rb') as stream:
                info = os.fstat(stream.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_size > 8 * 1024 ** 2:
                    raise ValueError('Unexpected application configuration file')
                content = stream.read(8 * 1024 ** 2 + 1)
                if len(content) > 8 * 1024 ** 2:
                    raise ValueError('Application configuration exceeds the size limit')
            with (destination / name).open('xb') as output:
                output.write(content)
            os.chmod(destination / name, 0o600)
    finally:
        os.close(directory)


def safe_unpack(archive, destination):
    """Never restore archive owners, links, devices, or executable scripts."""
    destination = Path(destination)
    destination.mkdir(mode=0o700)
    allowed = {'sub6-linux-amd64', 'resources', 'LICENSES', 'LICENSE',
               'THIRD_PARTY_NOTICES.md', 'Dockerfile.prebuilt', 'README.txt', 'build-info.json'}
    total, seen = 0, set()
    with tarfile.open(archive, 'r:gz') as tar:
        for item in tar:
            name = PurePosixPath(item.name)
            if (not name.parts or name.is_absolute() or '..' in name.parts
                    or '\\' in item.name or ':' in item.name or name.parts[0] not in allowed
                    or str(name) in seen or not (item.isdir() or item.isfile())):
                raise ValueError('Release contains an unsafe or unexpected archive member')
            seen.add(str(name))
            if item.size < 0 or item.size > 500 * 1024 ** 2:
                raise ValueError('Release member exceeds the size limit')
            total += item.size
            if total > 700 * 1024 ** 2:
                raise ValueError('Release archive exceeds the unpacked size limit')
            target = destination.joinpath(*name.parts)
            if item.isdir():
                target.mkdir(parents=True, exist_ok=True, mode=0o755)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(item) as source, target.open('xb') as output:
                    shutil.copyfileobj(source, output)
                os.chmod(target, 0o755 if item.name == 'sub6-linux-amd64' else 0o644)
    for required in ('sub6-linux-amd64', 'build-info.json', 'resources'):
        if not (destination / required).exists():
            raise ValueError('Release is missing a required runtime file')
    # The controller's private umask must not propagate root-only directories
    # into Docker COPY layers. Keep the outer build context private, while
    # runtime subdirectories remain traversable by the unprivileged app user.
    for path in destination.rglob('*'):
        if path.is_dir():
            os.chmod(path, 0o755)


def stage_routes(text, old_main, old_gateway, new_gateway):
    """Preserve the operator's proxy blocks, headers, TLS and stream settings."""
    if '@sub6_upgrade_models' in text:
        raise ValueError('A previous route transition requires operator review')
    text = text.replace(old_gateway, new_gateway)
    pattern = r'(?m)^([ \t]*)reverse_proxy ' + re.escape(old_main) + r' \{'
    matches = list(re.finditer(pattern, text))
    if not matches:
        raise ValueError('Expected current main upstream was not found')
    for match in reversed(matches):
        start, brace, depth = match.start(), text.index('{', match.start()), 1
        end = brace + 1
        while depth and end < len(text):
            depth += (text[end] == '{') - (text[end] == '}')
            end += 1
        if depth:
            raise ValueError('Unbalanced proxy configuration')
        block, indent = text[start:end], match.group(1)
        model = block.replace('reverse_proxy ' + old_main,
                              'reverse_proxy @sub6_upgrade_models ' + new_gateway, 1)
        prelude = (indent + '@sub6_upgrade_admin {\n' + indent + '    method POST PUT PATCH DELETE\n'
                   + indent + '    path /api/v1/admin/*\n' + indent + '}\n'
                   + indent + 'respond @sub6_upgrade_admin "Upgrade in progress; retry shortly" 503\n'
                   + indent + '@sub6_upgrade_models path ' + MODEL_PATHS + '\n' + model + '\n')
        text = text[:start] + prelude + text[start:]
    return text


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        parsed = urllib.parse.urlsplit(newurl)
        if parsed.scheme != 'https' or parsed.hostname not in {
                'github.com', 'objects.githubusercontent.com', 'release-assets.githubusercontent.com'}:
            raise ValueError('Untrusted release redirect')
        redirected = super().redirect_request(request, fp, code, message, headers, newurl)
        if redirected is not None:
            redirected.remove_header('Authorization')
        return redirected


class Controller:
    def __init__(self, config_path):
        self.config_path = Path(config_path)
        self.config = json.loads(self.config_path.read_text())
        self.root = Path(self.config['state_directory'])
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.token = Path(self.config['token_file']).read_text().strip()
        if len(self.token) < 32:
            raise ValueError('Controller token must contain at least 32 random characters')
        self.mutex = threading.RLock()
        self.busy = False
        self.state_path = self.root / 'state.json'
        self.state = json.loads(self.state_path.read_text()) if self.state_path.exists() else {'phase': 'idle'}
        if self.state.get('phase') not in TERMINAL:
            self.state.update(phase='failed', error='Controller restarted during an operation; operator review required')
            atomic_json(self.state_path, self.state)
        self.opener = urllib.request.build_opener(SafeRedirect())

    def run(self, args, timeout=120):
        result = subprocess.run(args, text=True, capture_output=True, timeout=timeout)
        if result.returncode:
            # Do not include command arguments or stderr: either can contain private config.
            raise RuntimeError('Deployment command failed: ' + args[0] + ' ' + args[1])
        return result.stdout

    def inspect(self, name):
        return json.loads(self.run(['docker', 'inspect', name]))[0]

    def status(self):
        with self.mutex:
            value = {k: self.state[k] for k in (
                'operation_id', 'phase', 'target_version', 'error') if k in self.state}
            value.update(managed=True, current_version=self.config['current']['version'],
                         versions=[{'version': v['version'], 'published_at': v.get('at', ''),
                                    'html_url': 'https://github.com/' + REPOSITORY + '/releases/tag/v' + v['version']}
                                   for v in self.config.get('history', [])[:3]
                                   if v['version'] != self.config['current']['version']])
            return value

    def installation(self, info):
        """A release may request only a transition already reviewed in this controller."""
        contract = info.get('managed_upgrade', {})
        actual = self.config.get('database_migrations_fingerprint',
                                 self.config['current']['migrations_fingerprint'])
        target = contract.get('migrations_fingerprint')
        result = {'managed': True, 'allowed': False, 'reason_code': 'migration_review_required',
                  'current_version': self.config['current']['version'], 'target_version': info['version']}
        if (contract.get('rolling_compatible') is not True
                or type(contract.get('controller_protocol')) is not int
                or contract.get('controller_protocol') not in (1, 2)
                or type(contract.get('deployment_probe_protocol')) is not int
                or contract.get('deployment_probe_protocol') != 1):
            return result
        if target == actual:
            return dict(result, allowed=True, reason_code='', schema_mode='unchanged')
        if (actual == PLATFORM_BASE and target == PLATFORM_SCHEMA
                and contract.get('controller_protocol') == 2
                and contract.get('online_migration_plan') == PLATFORM_PLAN):
            if not self.config.get('migration_database'):
                return dict(result, reason_code='migration_database_unconfigured')
            return dict(result, allowed=True, reason_code='', schema_mode=PLATFORM_PLAN)
        return result

    def schema_snapshot(self):
        database = self.config['migration_database']
        keys = ('container', 'user', 'database', 'data_directory')
        if any(not isinstance(database.get(k), str) or not database[k] for k in keys):
            raise ValueError('Migration database configuration is incomplete')
        # Only operator-owned configuration supplies a container or database name.
        query = """SET statement_timeout='5s'; SET lock_timeout='1s';
SELECT json_build_object(
 'read_only',current_setting('transaction_read_only'),
 'long_transactions',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()
    AND pid<>pg_backend_pid() AND xact_start<now()-interval '60 seconds'),
 'table_bytes',pg_relation_size('public.usage_logs'),
 'column',(SELECT json_build_object('type',data_type,'length',character_maximum_length,
    'nullable',is_nullable,'default',column_default) FROM information_schema.columns WHERE table_schema='public'
    AND table_name='usage_logs' AND column_name='platform'),
 'index',(SELECT json_build_object('valid',i.indisvalid,'definition',pg_get_indexdef(i.indexrelid))
    FROM pg_index i WHERE i.indexrelid=to_regclass('public.idx_usage_logs_platform_created')),
 'ledger',(SELECT coalesce(json_object_agg(filename,checksum),'{}'::json) FROM schema_migrations
    WHERE filename IN ('343_usage_log_platform_snapshot.sql','344_usage_log_platform_index_notx.sql')));"""
        args = ['docker', 'exec', '-i', database['container'], 'psql', '-X', '-qAt',
                '-v', 'ON_ERROR_STOP=1', '--username=' + database['user'], '--dbname=' + database['database']]
        process = subprocess.run(args, input=query, text=True, capture_output=True, timeout=15)
        if process.returncode:
            raise ValueError('Migration database preflight is unavailable; serving nodes were retained')
        value = json.loads(process.stdout.strip())
        if value['read_only'] != 'off':
            raise ValueError('Migration target must be the writable primary database')
        column = value.get('column')
        if column and column != {'type': 'character varying', 'length': 50, 'nullable': 'YES', 'default': None}:
            raise ValueError('The platform column does not match the reviewed nullable schema')
        if any(PLATFORM_MIGRATIONS.get(k) != v for k, v in value['ledger'].items()):
            raise ValueError('Reviewed migration checksums differ from the database ledger')
        return value

    def migration_preflight(self):
        value = self.schema_snapshot()
        database = self.config['migration_database']
        minimum = max(5 * 1024 ** 3, 2 * value['table_bytes'])
        if shutil.disk_usage(database['data_directory']).free < minimum:
            raise ValueError('The database disk needs more headroom for the concurrent index')
        if value['long_transactions']:
            raise ValueError('Long database transactions must finish before this online migration')
        return value

    def preflight(self, version):
        checked_version(version)
        _, _, _, info = self.release_metadata(version)
        result = self.installation(info)
        if result['allowed'] and result.get('schema_mode') == PLATFORM_PLAN:
            try:
                self.migration_preflight()
            except (ValueError, OSError, subprocess.SubprocessError):
                result.update(allowed=False, reason_code='database_preflight_failed')
        return result

    def phase(self, name, **fields):
        with self.mutex:
            self.state.update(phase=name, **fields)
            atomic_json(self.state_path, self.state)
        print(json.dumps({'operation_id': self.state.get('operation_id'), 'phase': name}), flush=True)

    def start(self, action, version):
        if action not in {'update', 'rollback', 'restart'}:
            raise ValueError('Unsupported deployment action')
        with self.mutex:
            current = self.config['current']
            if action == 'restart':
                version = current['version']
            elif action == 'rollback' and not version:
                versions = [v['version'] for v in self.config.get('history', []) if v['version'] != current['version']]
                if not versions:
                    raise ValueError('No retained deployment is available for rollback')
                version = versions[0]
            checked_version(version)
            if self.busy:
                if (self.state.get('action'), self.state.get('target_version')) == (action, version):
                    return self.status()
                raise ValueError('Another deployment operation is already running')
            if action == 'update' and tuple(map(int, version.split('.'))) <= tuple(map(int, current['version'].split('.'))):
                raise ValueError('The requested update is not newer than the active release')
            if action == 'rollback' and not any(v['version'] == version for v in self.config.get('history', [])):
                raise ValueError('Rollback is limited to retained verified deployments')
            if self.state.get('error', '').startswith('Controller restarted'):
                raise ValueError('The interrupted deployment needs operator review')
            self.busy = True
            self.state = {'operation_id': secrets.token_hex(8), 'action': action,
                          'target_version': version, 'phase': 'queued'}
            atomic_json(self.state_path, self.state)
            worker = threading.Thread(target=self.work, daemon=True)
            worker.start()
            return self.status()

    def fetch(self, url, limit, destination=None, api=False):
        headers = {'User-Agent': 'SUB6-managed-updater', 'Accept': 'application/vnd.github+json' if api else '*/*'}
        token_path = self.config.get('github_token_file')
        if api and token_path:
            headers['Authorization'] = 'Bearer ' + Path(token_path).read_text().strip()
        request = urllib.request.Request(url, headers=headers)
        try:
            with self.opener.open(request, timeout=60) as response:
                content = bytearray() if destination is None else None
                stream = open(destination, 'xb') if destination is not None else None
                count = 0
                try:
                    while chunk := response.read(256 * 1024):
                        count += len(chunk)
                        if count > limit:
                            raise ValueError('Release download exceeds its size limit')
                        if stream is not None:
                            stream.write(chunk)
                        else:
                            content.extend(chunk)
                finally:
                    if stream is not None:
                        stream.close()
                return bytes(content) if content is not None else None
        except (urllib.error.URLError, TimeoutError) as exc:
            raise RuntimeError('Release download is temporarily unavailable') from exc

    def release_metadata(self, version):
        base = 'https://api.github.com/repos/' + REPOSITORY
        release = json.loads(self.fetch(base + '/releases/latest', 2 * 1024 ** 2, api=True))
        if release.get('tag_name') != 'v' + version or release.get('draft') or release.get('prerelease'):
            raise ValueError('Only the current stable SUB6 release can be installed')
        names = {'archive': 'sub6_' + version + '_linux_amd64.tar.gz',
                 'checksums': 'checksums.txt', 'metadata': 'build-info.json'}
        assets = {a['name']: a for a in release.get('assets', [])}
        for name in names.values():
            expected = 'https://github.com/' + REPOSITORY + '/releases/download/v' + version + '/' + name
            if name not in assets or assets[name]['browser_download_url'] != expected:
                raise ValueError('A trusted SUB6 release asset is missing')
        sums = self.fetch(assets[names['checksums']]['browser_download_url'], 64 * 1024).decode('utf-8')
        metadata = self.fetch(assets[names['metadata']]['browser_download_url'], 64 * 1024)
        if hashlib.sha256(metadata).hexdigest() != release_checksum(sums, names['metadata']):
            raise ValueError('The release metadata checksum does not match')
        info = json.loads(metadata)
        if (info.get('distribution') != 'SUB6' or info.get('version') != version
                or info.get('build_type') != 'release' or info.get('target') != 'linux/amd64'
                or not re.fullmatch(r'[0-9a-f]{40}', info.get('source_commit', ''))):
            raise ValueError('The release build metadata is incompatible')
        obj = json.loads(self.fetch(base + '/git/ref/tags/v' + version, 64 * 1024, api=True))['object']
        for _ in range(4):
            if obj['type'] == 'commit':
                break
            if obj['type'] != 'tag':
                raise ValueError('Release tag does not resolve to a source commit')
            obj = json.loads(self.fetch(base + '/git/tags/' + obj['sha'], 64 * 1024, api=True))['object']
        if obj.get('type') != 'commit' or obj['sha'] != info['source_commit']:
            raise ValueError('The release tag and binary source commit differ')
        return assets, names, sums, info

    def release_image(self, version, directory):
        self.phase('preflight')
        assets, names, sums, info = self.release_metadata(version)
        decision = self.installation(info)
        if not decision['allowed']:
            raise ValueError('This release requires operator migration review before rolling deployment ('
                             + decision['reason_code'] + ')')
        if decision['schema_mode'] == PLATFORM_PLAN:
            self.migration_preflight()
        self.phase('downloading')
        archive = directory / names['archive']
        self.fetch(assets[names['archive']]['browser_download_url'], 500 * 1024 ** 2, archive)
        self.phase('verifying')
        if file_sha256(archive) != release_checksum(sums, names['archive']):
            raise ValueError('The release checksum does not match')
        source = directory / 'runtime'
        safe_unpack(archive, source)
        if json.loads((source / 'build-info.json').read_text()) != info:
            raise ValueError('Archive and separately verified release metadata differ')
        binary_hash = file_sha256(source / 'sub6-linux-amd64')
        if binary_hash != info.get('binary_sha256'):
            raise ValueError('The release binary does not match its build manifest')
        contract = info.get('managed_upgrade', {})
        dockerfile = source / 'Dockerfile.controller'
        dockerfile.write_text('ARG RUNTIME_IMAGE\nFROM $' + '{RUNTIME_IMAGE}\n'
                              'COPY --chmod=0755 sub6-linux-amd64 /app/sub2api\n'
                              'COPY resources/ /app/resources/\n'
                              'COPY LICENSES/ /app/LICENSES/\n'
                              'COPY THIRD_PARTY_NOTICES.md /app/THIRD_PARTY_NOTICES.md\n')
        image = 'sub6-managed:' + version + '-' + info['source_commit'][:12]
        self.run(['docker', 'build', '--network=none', '--pull=false', '-f', str(dockerfile),
                  '--build-arg', 'RUNTIME_IMAGE=' + self.config['runtime_image'],
                  '--label', 'org.opencontainers.image.source=https://github.com/' + REPOSITORY,
                  '--label', 'org.opencontainers.image.revision=' + info['source_commit'],
                  '--label', 'org.opencontainers.image.version=' + version,
                  '-t', image, str(source)], timeout=300)
        return {'version': version, 'image': image, 'source_commit': info['source_commit'],
                'binary_sha256': binary_hash, 'migrations_fingerprint': contract['migrations_fingerprint'],
                'deployment_probe_protocol': 1, 'schema_mode': decision['schema_mode']}

    def migrate_online(self, release, directory):
        """Run the official bounded CLI, never SQL or commands supplied by the browser."""
        self.phase('migrating')
        self.migration_preflight()
        spec = self.inspect(self.config['current']['main_container'])
        env = dict(line.split('=', 1) for line in spec['Config']['Env'] if '=' in line)
        env['PGOPTIONS'] = '-c lock_timeout=2000 -c statement_timeout=600000'
        env.pop('SUB6_UPDATE_CONTROLLER_SOCKET', None)
        env.pop('SUB6_UPDATE_CONTROLLER_TOKEN_FILE', None)
        if any('\n' in k + v or '\r' in k + v for k, v in env.items()):
            raise ValueError('Container environment cannot be represented safely')
        env_file = directory / 'migration.env'
        env_file.write_text(''.join(k + '=' + v + '\n' for k, v in env.items()))
        os.chmod(env_file, 0o600)
        data = directory / 'migration-data'
        source = next(m['Source'] for m in spec['Mounts'] if m['Destination'] == '/app/data')
        copy_runtime_config(source, data)
        os.chown(data, 1000, 1000)
        for path in data.iterdir():
            os.chown(path, 1000, 1000)
        name = 'sub6-migration-' + self.state['operation_id']
        command = ['docker', 'create', '--name', name, '--restart=no', '--user', '1000:1000',
                   '--network', self.config['primary_network'], '--memory', '768m',
                   '--env-file', str(env_file), '--mount', 'type=bind,src=' + str(data) + ',dst=/app/data',
                   '--entrypoint', '/app/sub2api', release['image'],
                   '--migrate-only', '--migration-timeout=10m']
        self.run(command)
        self.config['schema_transition'] = {'plan': PLATFORM_PLAN, 'to': PLATFORM_SCHEMA,
                                            'phase': 'running', 'operation_id': self.state['operation_id']}
        atomic_json(self.config_path, self.config)
        try:
            for network in self.config.get('additional_networks', []):
                self.run(['docker', 'network', 'connect', network, name])
            try:
                result = subprocess.run(['docker', 'start', '--attach', name], text=True,
                                        capture_output=True, timeout=620)
            except subprocess.TimeoutExpired:
                self.run(['docker', 'stop', '--time', '10', name])
                raise ValueError('Online migration timed out; the old serving nodes were retained') from None
            (directory / 'migration.log').write_text(result.stdout + result.stderr)
            exit_code = self.inspect(name)['State']['ExitCode']
            if result.returncode or exit_code:
                raise ValueError('Online migration did not complete; retry after database locks are released')
            self.verify_platform_schema()
            self.config['database_migrations_fingerprint'] = PLATFORM_SCHEMA
            self.config['schema_transition']['phase'] = 'complete'
            atomic_json(self.config_path, self.config)
        except Exception:
            self.config['schema_transition']['phase'] = 'incomplete'
            atomic_json(self.config_path, self.config)
            raise

    def verify_platform_schema(self):
        value = self.schema_snapshot()
        index = value.get('index') or {}
        if (not value.get('column') or value['ledger'] != PLATFORM_MIGRATIONS
                or index.get('valid') is not True
                or index.get('definition') != ('CREATE INDEX idx_usage_logs_platform_created '
                    'ON public.usage_logs USING btree (platform, created_at DESC, id DESC)')):
            raise ValueError('The migrated platform schema or ledger failed verification')

    def prepare_schema(self, release, action, directory):
        actual = self.config.get('database_migrations_fingerprint',
                                 self.config['current']['migrations_fingerprint'])
        target = release['migrations_fingerprint']
        if target == actual:
            return
        if actual == PLATFORM_BASE and target == PLATFORM_SCHEMA and release.get('schema_mode') == PLATFORM_PLAN:
            self.migrate_online(release, directory)
            return
        # Only the exact retained stable binary was tested against the expanded schema.
        if (action in ('rollback', 'restart') and actual == PLATFORM_SCHEMA and target == PLATFORM_BASE
                and release.get('source_commit') == 'b3f8199dc3cc1e1520214fdd77832fc01b36578e'):
            self.verify_platform_schema()
            return
        raise ValueError('Deployment would cross an unreviewed database migration boundary')

    def clone_instance(self, template, role, image, directory):
        name = 'sub6-' + role + '-' + self.state['operation_id']
        spec = self.inspect(template)
        env = dict(line.split('=', 1) for line in spec['Config']['Env'] if '=' in line)
        data = directory / (role + '-data')
        source = next(m['Source'] for m in spec['Mounts'] if m['Destination'] == '/app/data')
        copy_runtime_config(source, data)
        env['RUNTIME_NODE_ID'] = 'sub6-' + role + '-' + self.state['operation_id']
        env['SUB6_UPDATE_CONTROLLER_TOKEN_FILE'] = '/run/sub6-updater/token'
        if role == 'main':
            env['SUB6_UPDATE_CONTROLLER_SOCKET'] = '/run/sub6-updater/control.sock'
        else:
            env.pop('SUB6_UPDATE_CONTROLLER_SOCKET', None)
        env_file = directory / (role + '.env')
        if any('\n' in str(k) + str(v) or '\r' in str(k) + str(v) for k, v in env.items()):
            raise ValueError('Container environment cannot be represented safely')
        env_file.write_text(''.join(k + '=' + v + '\n' for k, v in env.items()))
        os.chmod(env_file, 0o600)
        host = spec['HostConfig']
        command = ['docker', 'create', '--name', name, '--network', self.config['primary_network'],
                   '--env-file', str(env_file), '--restart', 'unless-stopped', '--stop-timeout', '3600',
                   '--memory', str(host['Memory']), '--memory-swap', str(host['MemorySwap']),
                   '--ulimit', 'nofile=1048576:1048576', '--log-driver', 'local',
                   '--log-opt', 'max-size=20m', '--log-opt', 'max-file=5',
                   '--mount', 'type=bind,src=' + str(data) + ',dst=/app/data',
                   '--mount', 'type=bind,src=' + self.config['socket_directory'] + ',dst=/run/sub6-updater,readonly',
                   '--health-cmd', 'wget -q -O /dev/null http://127.0.0.1:1980/readyz',
                   '--health-interval', '10s', '--health-timeout', '5s', '--health-start-period', '60s',
                   '--label', 'org.sub6.managed=true']
        for extra in host.get('ExtraHosts') or []:
            command += ['--add-host', extra]
        command.append(image)
        self.run(command)
        for network in self.config.get('additional_networks', []):
            self.run(['docker', 'network', 'connect', network, name])
        self.run(['docker', 'start', name])
        ip = self.inspect(name)['NetworkSettings']['Networks'][self.config['primary_network']]['IPAddress']
        return name, ip + ':1980'

    def request_local(self, endpoint, path, token=False):
        headers = {'Authorization': 'Bearer ' + self.token} if token else {}
        request = urllib.request.Request('http://' + endpoint + path, headers=headers)
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status, response.read(64 * 1024)

    def ready(self, container, endpoint):
        for _ in range(120):
            if not self.inspect(container)['State']['Running']:
                raise RuntimeError('A candidate service exited before becoming ready')
            try:
                if self.request_local(endpoint, '/readyz')[0] == 200:
                    return
            except (urllib.error.URLError, TimeoutError):
                pass
            time.sleep(1)
        raise RuntimeError('A candidate service did not become ready')

    def validate_proxy(self, route, text, directory, suffix):
        candidate = directory / (route['name'] + '-' + suffix + '.Caddyfile')
        candidate.write_text(text)
        temp = '/tmp/sub6-managed-' + self.state['operation_id'] + '-' + suffix + '.Caddyfile'
        self.run(['docker', 'cp', str(candidate), route['container'] + ':' + temp])
        self.run(['docker', 'exec', route['container'], 'caddy', 'validate', '--config', temp, '--adapter', 'caddyfile'])

    def proxy(self, route, text, expected, directory, suffix):
        path = Path(route['path'])
        if path.read_text() != expected:
            raise RuntimeError('Proxy configuration changed concurrently; operator review required')
        self.validate_proxy(route, text, directory, suffix)
        path.write_text(text)  # preserve the file inode used by the existing bind mount
        try:
            self.run(['docker', 'exec', route['container'], 'caddy', 'reload', '--config',
                      route.get('container_path', '/etc/caddy/Caddyfile'), '--adapter', 'caddyfile'])
        except Exception:
            path.write_text(expected)
            self.run(['docker', 'exec', route['container'], 'caddy', 'reload', '--config',
                      route.get('container_path', '/etc/caddy/Caddyfile'), '--adapter', 'caddyfile'])
            raise

    def upstream_count(self, endpoint):
        count, found = 0, False
        for route in self.config['routes']:
            if route.get('admin_url'):
                with urllib.request.urlopen(route['admin_url'] + '/reverse_proxy/upstreams', timeout=5) as response:
                    values = json.load(response)
            else:
                values = json.loads(self.run(['docker', 'exec', route['container'], 'wget', '-qO-',
                                               'http://127.0.0.1:2019/reverse_proxy/upstreams']))
            for value in values:
                if value['address'] == endpoint:
                    count += value['num_requests']
                    found = True
        if not found:
            raise RuntimeError('Cannot prove the legacy main has drained')
        return count

    def wait_drain(self, current):
        self.phase('draining')
        zeros = 0
        deadline = time.monotonic() + self.config.get('drain_timeout_seconds', 1800)
        while time.monotonic() < deadline:
            if current.get('deployment_probe_protocol') == 0:
                # Only an explicitly reviewed legacy deployment can use Caddy's
                # counters. Its SPA may return 200 HTML for unknown paths.
                active = self.upstream_count(current['main_endpoint'])
            elif current.get('deployment_probe_protocol') == 1:
                _, raw = self.request_local(current['main_endpoint'], '/internal/deployment/status', token=True)
                active = json.loads(raw)['active_requests']
                if not isinstance(active, int) or isinstance(active, bool) or active < 0:
                    raise ValueError('Invalid active request counter')
            else:
                raise ValueError('The active deployment requires a reviewed drain protocol')
            zeros = zeros + 1 if active == 0 else 0
            if zeros >= 2:
                return
            time.sleep(2)
        raise RuntimeError('Existing requests are still active; deployment was not forced')

    def retire(self, containers):
        path = self.root / 'retired.json'
        values = json.loads(path.read_text()) if path.exists() else []
        for name in containers:
            if name not in values:
                values.append(name)
        atomic_json(path, values)

    def cleanup_retired(self):
        # Keep old WebSockets alive. Even an idle established connection prevents a stop.
        with self.mutex:
            if self.busy:
                return
            path = self.root / 'retired.json'
            values = json.loads(path.read_text()) if path.exists() else []
            keep = []
            for name in values:
                if name in (self.config['current']['main_container'], self.config['current']['gateway_container']):
                    continue
                try:
                    spec = self.inspect(name)
                    if not spec['State']['Running']:
                        continue
                    pid = spec['State']['Pid']
                    connections = 0
                    for family in ('tcp', 'tcp6'):
                        for row in Path('/proc/' + str(pid) + '/net/' + family).read_text().splitlines()[1:]:
                            parts = row.split()
                            if parts[1].split(':')[-1] == '07BC' and parts[3] == '01':
                                connections += 1
                    if connections:
                        keep.append(name)
                    else:
                        self.run(['docker', 'stop', '--time', '60', name], timeout=75)
                except Exception:
                    keep.append(name)
            atomic_json(path, keep)

    def work(self):
        directory = self.root / 'operations' / self.state['operation_id']
        directory.mkdir(parents=True, mode=0o700)
        current = copy.deepcopy(self.config['current'])
        originals, staged_routes, applied, gateway, new_main = {}, {}, {}, None, None
        old_stopped = False
        committed = False
        previous_config = copy.deepcopy(self.config)
        try:
            if shutil.disk_usage(self.root).free < 5 * 1024 ** 3:
                raise ValueError('The deployment host needs at least 5 GiB free')
            for name in ('main_container', 'gateway_container'):
                if not self.inspect(current[name])['State']['Running']:
                    raise ValueError('The active deployment does not match the controller configuration')
            action, version = self.state['action'], self.state['target_version']
            if action == 'update':
                release = self.release_image(version, directory)
            elif action == 'restart':
                release = copy.deepcopy(current)
            else:
                release = copy.deepcopy(next(v for v in self.config['history'] if v['version'] == version))
                self.inspect(release['image'])
            self.prepare_schema(release, action, directory)
            # Preserve real database state even if a later candidate or cutover fails.
            previous_config = copy.deepcopy(self.config)
            self.phase('starting')
            gateway = 'sub6-gateway-' + self.state['operation_id']
            gateway, gateway_endpoint = self.clone_instance(current['gateway_container'], 'gateway', release['image'], directory)
            self.ready(gateway, gateway_endpoint)
            for route in self.config['routes']:
                original = Path(route['path']).read_text()
                originals[route['name']] = original
                (directory / (route['name'] + '-before.Caddyfile')).write_text(original)
                staged = stage_routes(original, current['main_endpoint'], current['gateway_endpoint'], gateway_endpoint)
                staged_routes[route['name']] = staged
                self.validate_proxy(route, staged, directory, 'preflight')
            for route in self.config['routes']:
                staged, original = staged_routes[route['name']], originals[route['name']]
                self.proxy(route, staged, original, directory, 'stage')
                applied[route['name']] = staged
            self.wait_drain(current)
            self.phase('switching')
            old_stopped = True
            self.run(['docker', 'stop', '--time', '3600', current['main_container']], timeout=3615)
            new_main = 'sub6-main-' + self.state['operation_id']
            new_main, main_endpoint = self.clone_instance(current['main_container'], 'main', release['image'], directory)
            self.ready(new_main, main_endpoint)
            # Probe both business protocol families without spending upstream credits.
            for endpoint in (main_endpoint, gateway_endpoint):
                try:
                    self.request_local(endpoint, '/v1/models')
                    raise RuntimeError('Candidate unexpectedly permits unauthenticated model access')
                except urllib.error.HTTPError as error:
                    if error.code != 401:
                        raise
            finals = {name: text.replace(current['main_endpoint'], main_endpoint).replace(
                current['gateway_endpoint'], gateway_endpoint) for name, text in originals.items()}
            for route in self.config['routes']:
                self.validate_proxy(route, finals[route['name']], directory, 'final-preflight')
            for route in self.config['routes']:
                final = finals[route['name']]
                self.proxy(route, final, applied[route['name']], directory, 'final')
                applied[route['name']] = final
            release.update(main_container=new_main, gateway_container=gateway,
                           main_endpoint=main_endpoint, gateway_endpoint=gateway_endpoint,
                           at=datetime.datetime.now(datetime.timezone.utc).isoformat())
            with self.mutex:
                history = [current] + [v for v in self.config.get('history', []) if v['version'] != current['version']]
                self.config['history'] = history[:3]
                self.config['current'] = release
                self.write_compose(directory)
                self.retire([current['gateway_container']])
                atomic_json(self.config_path, self.config)
                committed = True
            self.phase('completed', completed_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
        except Exception as error:
            if committed:
                self.phase('failed', error='The new deployment is active; final status persistence needs operator review')
                return
            schema_state = {k: self.config[k] for k in ('schema_transition', 'database_migrations_fingerprint')
                            if k in self.config}
            self.config = previous_config
            self.config.update(schema_state)
            atomic_json(self.config_path, self.config)
            recovered = True
            if old_stopped:
                try:
                    if new_main is not None:
                        try:
                            running = self.inspect(new_main)['State']['Running']
                        except Exception:
                            running = False
                        if running:
                            # Withdraw the failed candidate before stopping it;
                            # model requests keep using the ready gateway.
                            for route in self.config['routes']:
                                if route['name'] in applied:
                                    self.proxy(route, staged_routes[route['name']], applied[route['name']], directory, 'withdraw')
                                    applied[route['name']] = staged_routes[route['name']]
                            self.run(['docker', 'stop', '--time', '3600', new_main], timeout=3615)
                    self.run(['docker', 'start', current['main_container']])
                    self.ready(current['main_container'], current['main_endpoint'])
                except Exception:
                    recovered = False
            for route in self.config['routes']:
                if route['name'] in applied:
                    try:
                        self.proxy(route, originals[route['name']], applied[route['name']], directory, 'restore')
                    except Exception:
                        recovered = False
            if gateway is not None:
                self.retire([gateway])
            try:
                self.write_compose(directory)
            except Exception:
                recovered = False
            self.phase('failed', error=str(error) if recovered else 'Deployment needs operator recovery; previous routes could not be fully restored')
        finally:
            with self.mutex:
                self.busy = False

    def write_compose(self, directory):
        # Persist the actual new image/data/network state, so a later recreate cannot
        # silently return to the old image. No environment values go to web responses.
        services = {}
        for role in ('main', 'gateway'):
            spec = self.inspect(self.config['current'][role + '_container'])
            host = spec['HostConfig']
            services[role] = {
                'image': spec['Config']['Image'], 'container_name': spec['Name'].lstrip('/'),
                'restart': 'unless-stopped', 'environment': spec['Config']['Env'],
                'volumes': [m['Source'] + ':' + m['Destination'] + (':ro' if not m['RW'] else '') for m in spec['Mounts']],
                'networks': {n: {'ipv4_address': v['IPAddress']} for n, v in spec['NetworkSettings']['Networks'].items()},
                'extra_hosts': host.get('ExtraHosts') or [], 'mem_limit': host['Memory'],
                'memswap_limit': host['MemorySwap'], 'stop_grace_period': '1h',
                'ulimits': {'nofile': {'soft': 1048576, 'hard': 1048576}},
                'logging': {'driver': 'local', 'options': {'max-size': '20m', 'max-file': '5'}}}
        value = {'name': 'sub6-managed', 'services': services,
                 'networks': {n: {'external': True, 'name': n} for n in [self.config['primary_network']] + self.config.get('additional_networks', [])}}
        atomic_json(self.root / 'compose.active.json', value)
        if path := self.config.get('compose_output'):
            atomic_json(path, value)


class RequestHandler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def answer(self, code, value):
        body = json.dumps(value).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)

    def authenticated(self):
        expected = 'Bearer ' + self.server.controller.token
        if not hmac.compare_digest(self.headers.get('Authorization', '').encode(), expected.encode()):
            self.answer(401, {'error': 'Unauthorized'})
            return False
        return True

    def do_GET(self):
        if self.authenticated():
            if self.path == '/v1/status':
                self.answer(200, self.server.controller.status())
            elif self.path.startswith('/v1/preflight?'):
                try:
                    query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query, strict_parsing=True)
                    if set(query) != {'version'} or len(query['version']) != 1:
                        raise ValueError('Invalid preflight version')
                    self.answer(200, self.server.controller.preflight(query['version'][0]))
                except (ValueError, KeyError, OSError, RuntimeError, subprocess.SubprocessError):
                    self.answer(200, {'managed': True, 'allowed': False, 'reason_code': 'preflight_unavailable'})
            else:
                self.answer(404, {'error': 'Unknown controller endpoint'})

    def do_POST(self):
        if not self.authenticated():
            return
        if self.path != '/v1/operations':
            self.answer(404, {'error': 'Unknown controller endpoint'})
            return
        try:
            size = int(self.headers.get('Content-Length', '0'))
            if size < 1 or size > 1024:
                raise ValueError('Invalid operation payload size')
            value = json.loads(self.rfile.read(size))
            if set(value) != {'action', 'version'}:
                raise ValueError('Only action and version are accepted')
            self.answer(202, self.server.controller.start(value['action'], value['version']))
        except (ValueError, TypeError, KeyError):
            self.answer(409, {'error': 'Invalid, unavailable or conflicting deployment operation'})


class UnixServer(getattr(socketserver, 'ThreadingUnixStreamServer', socketserver.ThreadingTCPServer)):
    daemon_threads = True


def main():
    if os.name != 'posix':
        raise RuntimeError('The deployment controller requires Linux')
    import fcntl  # deployment is Linux-only; pure validation tests also run elsewhere
    parser = argparse.ArgumentParser()
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    # Acquire ownership before reading/mutating an earlier operation's state.
    initial = json.loads(Path(args.config).read_text())
    state_dir = Path(initial['state_directory'])
    state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    lock = open(state_dir / 'controller.lock', 'a')
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    controller = Controller(args.config)
    socket_dir = Path(controller.config['socket_directory'])
    socket_dir.mkdir(mode=0o750, parents=True, exist_ok=True)
    os.chown(socket_dir, 0, controller.config.get('socket_gid', 1000))
    os.chmod(socket_dir, 0o750)
    sock = socket_dir / 'control.sock'
    if sock.exists():
        if not sock.is_socket():
            raise ValueError('Refusing to replace a non-socket control path')
        sock.unlink()
    with UnixServer(str(sock), RequestHandler) as server:
        server.controller = controller
        os.chown(sock, 0, controller.config.get('socket_gid', 1000))
        os.chmod(sock, 0o660)
        def cleaner():
            while True:
                time.sleep(30)
                try:
                    controller.cleanup_retired()
                except Exception:
                    print('Retired connection cleanup deferred', flush=True)
        threading.Thread(target=cleaner, daemon=True).start()
        server.serve_forever(poll_interval=1)


if __name__ == '__main__':
    main()
