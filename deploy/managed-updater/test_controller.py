import copy
import importlib.util
import io
import hashlib
import json
import os
import stat
from pathlib import Path
import tarfile
import tempfile
import unittest
import urllib.error
import urllib.request
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('controller', Path(__file__).with_name('controller.py'))
controller = importlib.util.module_from_spec(spec)
spec.loader.exec_module(controller)


class ReleaseBoundaryTests(unittest.TestCase):
    def archive(self, path, members):
        with tarfile.open(path, 'w:gz') as tar:
            for name, kind in members:
                member = tarfile.TarInfo(name)
                member.type = kind
                if kind == tarfile.REGTYPE:
                    member.size = 1
                    tar.addfile(member, io.BytesIO(b'x'))
                else:
                    member.linkname = '/outside'
                    tar.addfile(member)

    def test_rejects_escape_links_devices_duplicate_and_foreign_payload(self):
        for name, kind in [('../outside', tarfile.REGTYPE), ('/outside', tarfile.REGTYPE),
                           ('resources/link', tarfile.SYMTYPE), ('resources/link', tarfile.LNKTYPE),
                           ('resources/device', tarfile.CHRTYPE), ('install.sh', tarfile.REGTYPE),
                           ('resources\\escape', tarfile.REGTYPE), ('C:/escape', tarfile.REGTYPE)]:
            with self.subTest(name=name, kind=kind), tempfile.TemporaryDirectory() as tmp:
                path = Path(tmp) / 'release.tar.gz'
                self.archive(path, [(name, kind)])
                with self.assertRaises(ValueError):
                    controller.safe_unpack(path, Path(tmp) / 'out')
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'release.tar.gz'
            self.archive(path, [('sub6-linux-amd64', tarfile.REGTYPE)] * 2)
            with self.assertRaises(ValueError):
                controller.safe_unpack(path, Path(tmp) / 'out')

    @unittest.skipUnless(os.name == 'posix', 'POSIX directory modes')
    def test_private_umask_does_not_make_runtime_directories_root_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            archive = root / 'release.tar.gz'
            self.archive(archive, [('sub6-linux-amd64', tarfile.REGTYPE),
                                   ('build-info.json', tarfile.REGTYPE),
                                   ('resources/model-pricing/nested/prices.json', tarfile.REGTYPE),
                                   ('LICENSES', tarfile.DIRTYPE)])
            previous = os.umask(0o077)
            try:
                controller.safe_unpack(archive, root / 'out')
            finally:
                os.umask(previous)
            self.assertEqual(0o700, stat.S_IMODE((root / 'out').stat().st_mode))
            for name in ['resources', 'resources/model-pricing', 'resources/model-pricing/nested', 'LICENSES']:
                self.assertEqual(0o755, stat.S_IMODE((root / 'out' / name).stat().st_mode))
            self.assertEqual(0o644, stat.S_IMODE((root / 'out/resources/model-pricing/nested/prices.json').stat().st_mode))
            self.assertEqual(0o755, stat.S_IMODE((root / 'out/sub6-linux-amd64').stat().st_mode))

    def test_versions_cannot_be_urls_shell_options_or_paths(self):
        for value in ['latest', '../0.1.3', '0.1.3; echo bad', '--privileged', 'https://example.invalid', None, 13]:
            with self.assertRaises(ValueError):
                controller.checked_version(value)
        self.assertEqual('0.1.3', controller.checked_version('0.1.3'))

    def test_redirects_cannot_escape_https_release_hosts(self):
        handler = controller.SafeRedirect()
        for url in ['http://github.com/file', 'https://attacker.invalid/file', 'https://github.com.attacker.invalid/file']:
            with self.assertRaises(ValueError):
                handler.redirect_request(None, None, 302, '', {}, url)

    @unittest.skipUnless(hasattr(os, 'O_NOFOLLOW'), 'Linux no-follow file descriptors')
    def test_app_owned_symlink_is_not_copied_with_host_privileges(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / 'data'
            source.mkdir()
            (root / 'private').write_text('not application data')
            (source / 'config.yaml').symlink_to(root / 'private')
            with self.assertRaises(OSError):
                controller.copy_runtime_config(source, root / 'out')
            self.assertFalse((root / 'out/config.yaml').exists())


class MigrationReleaseBoundaryTests(unittest.TestCase):
    def test_verified_release_cannot_cross_the_stable_migration_boundary(self):
        old_fingerprint = 'a6990935dd92d8132f2d487ba2b3320ac10d8819e72363db2d240bca23e4063f'
        binary = b'synthetic binary; never executed'
        commit = 'a' * 40
        for same_schema, rolling in [(False, True), (True, False), (True, True)]:
            with self.subTest(same_schema=same_schema, rolling=rolling), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                fingerprint = old_fingerprint if same_schema else 'b' * 64
                metadata = {'distribution': 'SUB6', 'version': '0.1.6', 'build_type': 'release',
                            'target': 'linux/amd64', 'source_commit': commit,
                            'binary_sha256': hashlib.sha256(binary).hexdigest(),
                            'managed_upgrade': {'controller_protocol': 1, 'deployment_probe_protocol': 1,
                                                'rolling_compatible': rolling,
                                                'migrations_fingerprint': fingerprint}}
                archive_bytes = io.BytesIO()
                with tarfile.open(fileobj=archive_bytes, mode='w:gz') as tar:
                    for name, data in [('sub6-linux-amd64', binary),
                                       ('build-info.json', json.dumps(metadata).encode()),
                                       ('resources/fixture.json', b'{}')]:
                        member = tarfile.TarInfo(name)
                        member.size = len(data)
                        tar.addfile(member, io.BytesIO(data))
                archive = archive_bytes.getvalue()
                archive_name = 'sub6_0.1.6_linux_amd64.tar.gz'
                download = 'https://github.com/zhang2580384/SUB6/releases/download/v0.1.6/'
                release = {'tag_name': 'v0.1.6', 'assets': [
                    {'name': name, 'browser_download_url': download + name}
                    for name in [archive_name, 'checksums.txt']]}
                values = {'/releases/latest': json.dumps(release).encode(),
                          '/git/ref/tags/v0.1.6': json.dumps({'object': {'type': 'commit', 'sha': commit}}).encode(),
                          '/checksums.txt': (hashlib.sha256(archive).hexdigest() + '  ' + archive_name + '\n').encode(),
                          '/' + archive_name: archive}
                c = object.__new__(controller.Controller)
                c.config = {'current': {'version': '0.1.5', 'migrations_fingerprint': old_fingerprint},
                            'runtime_image': 'local-reviewed-runtime'}
                c.phase = lambda *args, **kwargs: None
                commands = []
                c.run = lambda args, **kwargs: commands.append(args)

                def fetch(url, limit, destination=None, api=False):
                    data = next(value for suffix, value in values.items() if url.endswith(suffix))
                    if destination:
                        destination.write_bytes(data)
                    else:
                        return data
                c.fetch = fetch
                if same_schema and rolling:
                    result = c.release_image('0.1.6', root)
                    self.assertEqual(old_fingerprint, result['migrations_fingerprint'])
                    self.assertEqual(1, len(commands))
                else:
                    with self.assertRaisesRegex(ValueError, 'operator migration review'):
                        c.release_image('0.1.6', root)
                    self.assertEqual([], commands, 'migration mismatch must precede Docker build or rollout')
                self.assertEqual(old_fingerprint, c.config['current']['migrations_fingerprint'])


class RouteTests(unittest.TestCase):
    def test_stage_keeps_stream_transport_and_trusted_headers(self):
        original = '''example.invalid {
    reverse_proxy @ws 127.0.0.1:1901 {
        stream_close_delay 24h
    }
    reverse_proxy 172.20.90.2:1980 {
        stream_close_delay 24h
        header_up X-Trusted-Route local
        transport http {
            dial_timeout 10s
        }
    }
}
'''
        staged = controller.stage_routes(original, '172.20.90.2:1980', '127.0.0.1:1901', '172.20.90.3:1980')
        self.assertIn('reverse_proxy @ws 172.20.90.3:1980', staged)
        self.assertIn('reverse_proxy @sub6_upgrade_models 172.20.90.3:1980', staged)
        self.assertIn('reverse_proxy 172.20.90.2:1980', staged)
        self.assertEqual(3, staged.count('stream_close_delay 24h'))
        self.assertEqual(2, staged.count('header_up X-Trusted-Route local'))
        self.assertIn('respond @sub6_upgrade_admin', staged)
        with self.assertRaises(ValueError):
            controller.stage_routes(staged, '172.20.90.2:1980', '127.0.0.1:1901', '172.20.90.4:1980')


class FakeController(controller.Controller):
    def __init__(self, root, fail_main=False):
        self.events = []
        self.fail_main = fail_main
        route = root / 'Caddyfile'
        route.write_text('example.invalid {\n    reverse_proxy 172.20.90.2:1980 {\n        stream_close_delay 24h\n    }\n}\n')
        (root / 'token').write_text('t' * 64)
        current = {'version': '0.1.2', 'image': 'old-image', 'main_container': 'old-main',
                   'gateway_container': 'old-gateway', 'main_endpoint': '172.20.90.2:1980',
                   'gateway_endpoint': '127.0.0.1:1901', 'migrations_fingerprint': 'same-migrations'}
        config = {'state_directory': str(root), 'token_file': str(root / 'token'),
                  'current': current, 'routes': [{'name': 'origin', 'container': 'proxy', 'path': str(route)}]}
        (root / 'config.json').write_text(json.dumps(config))
        super().__init__(root / 'config.json')
        self.state = {'operation_id': 'test', 'action': 'update', 'target_version': '0.1.3', 'phase': 'queued'}
        self.busy = True
        self.running = {'old-main': True, 'old-gateway': True}

    def run(self, args, timeout=120):
        self.events.append(tuple(args))
        if args[:2] == ['docker', 'stop']:
            self.running[args[-1]] = False
        elif args[:2] == ['docker', 'start']:
            self.running[args[-1]] = True
        return ''

    def inspect(self, name):
        return {'State': {'Running': self.running.get(name, False)}}

    def release_image(self, version, directory):
        return {'version': version, 'image': 'new-image', 'migrations_fingerprint': 'same-migrations'}

    def clone_instance(self, template, role, image, directory):
        name = 'sub6-' + role + '-test'
        self.running[name] = True
        self.events.append(('create', role))
        return name, '172.20.90.' + ('3' if role == 'gateway' else '4') + ':1980'

    def ready(self, name, endpoint):
        self.events.append(('ready', name))
        if name == 'sub6-main-test' and self.fail_main:
            raise ValueError('Candidate readiness failed')

    def request_local(self, endpoint, path, token=False):
        raise urllib.error.HTTPError('http://local', 401, 'unauthorized', {}, None)

    def wait_drain(self, current):
        self.events.append(('drained', current['main_container']))

    def write_compose(self, directory):
        controller.atomic_json(self.root / 'compose.active.json', {'version': self.config['current']['version']})


class RolloutTests(unittest.TestCase):
    def test_stop_timeout_after_actual_stop_still_recovers_old_main(self):
        with tempfile.TemporaryDirectory() as tmp:
            c = FakeController(Path(tmp))
            run = c.run
            def stop_then_timeout(args, timeout=120):
                result = run(args, timeout)
                if args[:2] == ['docker', 'stop'] and args[-1] == 'old-main':
                    raise TimeoutError('stop command timed out after stopping')
                return result
            c.run = stop_then_timeout
            c.work()
            self.assertEqual('failed', c.state['phase'])
            self.assertTrue(c.running['old-main'])
            self.assertEqual('0.1.2', c.config['current']['version'])

    def test_only_explicit_legacy_drain_uses_proxy_counters(self):
        with tempfile.TemporaryDirectory() as tmp:
            c = FakeController(Path(tmp))
            current = c.config['current']
            current['deployment_probe_protocol'] = 0
            c.upstream_count = lambda endpoint: 0
            with patch.object(controller.time, 'sleep'):
                controller.Controller.wait_drain(c, current)
            current['deployment_probe_protocol'] = 1
            # Broken/auth-failed probes must never fall back to a zero proxy count.
            with self.assertRaises(urllib.error.HTTPError):
                controller.Controller.wait_drain(c, current)
            c.request_local = lambda *args, **kwargs: (200, b'<html>SPA</html>')
            with self.assertRaises(ValueError):
                controller.Controller.wait_drain(c, current)

    def test_success_drains_before_stopping_and_persists_both_nodes(self):
        with tempfile.TemporaryDirectory() as tmp:
            c = FakeController(Path(tmp))
            c.work()
            self.assertEqual('completed', c.state['phase'])
            self.assertEqual('0.1.3', c.config['current']['version'])
            self.assertIn('sub6-main-test', c.config['current']['main_container'])
            self.assertIn('sub6-gateway-test', c.config['current']['gateway_container'])
            stop = ('docker', 'stop', '--time', '3600', 'old-main')
            self.assertLess(c.events.index(('drained', 'old-main')), c.events.index(stop))
            self.assertTrue(c.running['old-gateway'], 'existing WebSockets must survive')
            self.assertFalse(c.running['old-main'])
            self.assertEqual('0.1.3', json.loads((Path(tmp) / 'config.json').read_text())['current']['version'])
            self.assertNotIn('@sub6_upgrade_models', (Path(tmp) / 'Caddyfile').read_text())

    def test_failed_candidate_restores_old_main_routes_and_persistent_config(self):
        with tempfile.TemporaryDirectory() as tmp:
            c = FakeController(Path(tmp), fail_main=True)
            original = (Path(tmp) / 'Caddyfile').read_text()
            c.work()
            self.assertEqual('failed', c.state['phase'])
            self.assertTrue(c.running['old-main'])
            self.assertTrue(c.running['old-gateway'])
            self.assertFalse(c.running['sub6-main-test'])
            self.assertEqual(original, (Path(tmp) / 'Caddyfile').read_text())
            self.assertEqual('0.1.2', c.config['current']['version'])
            self.assertEqual('0.1.2', json.loads((Path(tmp) / 'compose.active.json').read_text())['version'])
            self.assertEqual(['sub6-gateway-test'], json.loads((Path(tmp) / 'retired.json').read_text()))

    def test_unreviewed_rollback_and_conflicting_operations_cannot_start(self):
        with tempfile.TemporaryDirectory() as tmp:
            c = FakeController(Path(tmp))
            with self.assertRaises(ValueError):
                c.start('rollback', '0.1.1')
            with self.assertRaises(ValueError):
                c.start('update', '0.1.4')
            self.assertEqual('test', c.start('update', '0.1.3')['operation_id'])


if __name__ == '__main__':
    unittest.main()
