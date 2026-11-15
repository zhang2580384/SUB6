"""Real Docker/PostgreSQL acceptance, exclusively against disposable CI data."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time


def main():
    if os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('SUB6_ONLINE_UPGRADE_FIXTURE') != '1':
        raise RuntimeError('This fixture runs only in explicit isolated GitHub CI')
    binary, stable = map(lambda p: Path(p).resolve(), sys.argv[1:3])
    spec = importlib.util.spec_from_file_location('controller', Path(__file__).with_name('controller.py'))
    controller = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(controller)
    database = 'sub6_online_upgrade_fixture'
    user = 'sub6_test'
    names = subprocess.check_output(['docker', 'ps', '--filter', 'ancestor=postgres:18-alpine',
                                     '--format', '{{.ID}}'], text=True).splitlines()
    assert len(names) == 1, 'Expected exactly one disposable CI PostgreSQL service'
    postgres = names[0]
    inspection = json.loads(subprocess.check_output(['docker', 'inspect', postgres]))[0]
    network, address = next(iter(inspection['NetworkSettings']['Networks'].items()))
    image, template = 'sub6-online-upgrade-fixture:local', 'sub6-online-upgrade-fixture-main'

    def sql(query):
        return subprocess.check_output(['docker', 'exec', '-i', postgres, 'psql', '-X', '-qAt',
            '-v', 'ON_ERROR_STOP=1', '-U', user, '-d', database], input=query, text=True).strip()

    def run(args, **kwargs):
        subprocess.run(list(map(str, args)), check=True, **kwargs)

    env = {**os.environ, 'DATABASE_DBNAME': database}
    stop, writes = threading.Event(), []
    writer = holder = None
    with tempfile.TemporaryDirectory(prefix='sub6-online-fixture-') as tmp:
        root = Path(tmp)
        try:
            run(['docker', 'exec', postgres, 'createdb', '-U', user, database])
            run([stable, '--migrate-only', '--migration-timeout=5m'], env=env)
            assert sql("select count(*) from schema_migrations where filename like '343%' or filename like '344%'") == '0'
            sql('CREATE TABLE fixture_billing(id bigint generated always as identity, amount numeric NOT NULL)')
            (root / 'sub6').write_bytes(binary.read_bytes())
            (root / 'Dockerfile').write_text('FROM postgres:18-alpine\nCOPY --chmod=0755 sub6 /app/sub2api\n')
            run(['docker', 'build', '--network=none', '--pull=false', '-t', image, root])
            data = root / 'old-data'
            data.mkdir()
            token = root / 'token'
            token.write_text('fixture-' * 8)
            run(['docker', 'create', '--name', template, '--network', network,
                 '--mount', f'type=bind,src={data},dst=/app/data',
                 '-e', 'DATABASE_HOST=' + address['IPAddress'], '-e', 'DATABASE_PORT=5432',
                 '-e', 'DATABASE_USER=' + user, '-e', 'DATABASE_PASSWORD=sub6-test-only',
                 '-e', 'DATABASE_DBNAME=' + database, '-e', 'DATABASE_SSLMODE=disable',
                 '--entrypoint', '/bin/sh', image, '-c', 'sleep 600'])
            run(['docker', 'start', template])
            config = {'state_directory': str(root / 'state'), 'token_file': str(token),
                      'primary_network': network,
                      'migration_database': {'container': postgres, 'user': user, 'database': database,
                                             'data_directory': str(root)},
                      'current': {'version': '0.1.5', 'main_container': template,
                                  'migrations_fingerprint': controller.PLATFORM_BASE}}
            config_path = root / 'config.json'
            config_path.write_text(json.dumps(config))
            c = controller.Controller(config_path)
            release = {'image': image, 'migrations_fingerprint': controller.PLATFORM_SCHEMA,
                       'schema_mode': controller.PLATFORM_PLAN}
            # A short old-reader transaction must time out the DDL rather than block traffic indefinitely.
            holder = subprocess.Popen(['docker', 'exec', '-i', postgres, 'psql', '-X', '-qAt', '-U', user,
                '-d', database, '-c', 'BEGIN; LOCK TABLE usage_logs IN ACCESS SHARE MODE; SELECT pg_sleep(8); COMMIT;'])
            for _ in range(100):
                if int(sql("select count(*) from pg_stat_activity where datname=current_database() and query like 'BEGIN; LOCK TABLE%' and state='active'")):
                    break
                time.sleep(.05)
            else:
                raise AssertionError('Fixture reader did not acquire its transaction')
            c.state = {'operation_id': 'locked', 'phase': 'queued'}
            locked = root / 'locked'
            locked.mkdir()
            start = time.monotonic()
            try:
                c.migrate_online(release, locked)
            except ValueError as error:
                assert 'did not complete' in str(error), error
            else:
                raise AssertionError('Expected the configured database lock timeout')
            assert time.monotonic() - start < 7, 'PGOPTIONS lock timeout was not enforced'
            assert sql("select count(*) from schema_migrations where filename like '343%' or filename like '344%'") == '0'
            assert c.config['current']['migrations_fingerprint'] == controller.PLATFORM_BASE
            assert c.config['schema_transition']['phase'] == 'incomplete'
            holder.wait(timeout=15)

            def write_old_format():
                while not stop.is_set():
                    try:
                        sql('INSERT INTO fixture_billing(amount) VALUES(0.02); SELECT count(*) FROM usage_logs')
                        writes.append(True)
                    except Exception:
                        writes.append(False)
                    stop.wait(.03)

            writer = threading.Thread(target=write_old_format)
            writer.start()
            c.state = {'operation_id': 'success', 'phase': 'queued'}
            success = root / 'success'
            success.mkdir()
            c.migrate_online(release, success)
            stop.set()
            writer.join(timeout=10)
            assert writes and all(writes), writes
            assert c.config['database_migrations_fingerprint'] == controller.PLATFORM_SCHEMA
            assert c.config['current']['migrations_fingerprint'] == controller.PLATFORM_BASE
            c.verify_platform_schema()
            # The exact stable reader accepts the expanded schema and retains all ledger entries.
            run([stable, '--migrate-only', '--migration-timeout=5m'], env=env)
            c.prepare_schema({'migrations_fingerprint': controller.PLATFORM_BASE,
                'source_commit': 'b3f8199dc3cc1e1520214fdd77832fc01b36578e'}, 'rollback', root)
            assert int(sql('SELECT count(*) FROM fixture_billing')) == len(writes)
            assert json.loads(subprocess.check_output(['docker', 'inspect', template]))[0]['State']['Running']
            print(json.dumps({'result': 'pass', 'lock_timeout': True, 'retry': True,
                              'old_format_writes': len(writes), 'stable_reader': True,
                              'ledger_and_index_verified': True, 'old_template_retained': True}))
        finally:
            stop.set()
            if writer:
                writer.join(timeout=10)
            if holder and holder.poll() is None:
                holder.wait(timeout=15)
            for name in (template, 'sub6-migration-locked', 'sub6-migration-success'):
                subprocess.run(['docker', 'rm', '-f', name], check=False)
            subprocess.run(['docker', 'image', 'rm', image], check=False)
            subprocess.run(['docker', 'exec', postgres, 'dropdb', '-U', user, '--if-exists', database], check=False)


if __name__ == '__main__':
    main()
