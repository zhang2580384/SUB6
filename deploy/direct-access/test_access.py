"""Integration tests against a disposable loopback PostgreSQL database.

Set SUB6_REVIEW_DATABASE_URL to a dedicated database named sub6_review.
Never run this fixture against an application database.
"""
import importlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
import json
import os
from pathlib import Path
import unittest
import urllib.parse
from unittest.mock import patch

import psycopg


class AccessTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dsn = os.environ['SUB6_REVIEW_DATABASE_URL']
        parsed = urllib.parse.urlsplit(cls.dsn)
        if parsed.hostname != '127.0.0.1' or parsed.path != '/sub6_review':
            raise RuntimeError('Only the disposable loopback sub6_review database is allowed')
        with psycopg.connect(cls.dsn, autocommit=True) as db:
            db.execute('DROP SCHEMA IF EXISTS direct_access CASCADE')
            db.execute('DROP TABLE IF EXISTS public.redeem_codes, public.users CASCADE')
            db.execute('CREATE TABLE users(id bigint PRIMARY KEY,email text,username text,status text,total_recharged numeric,deleted_at timestamptz)')
            db.execute('CREATE TABLE redeem_codes(used_by bigint,type text,status text,value numeric,used_at timestamptz)')
            db.execute("INSERT INTO users VALUES (1,'customer@example.com','Customer','active',100,NULL),(2,'other@example.com','Other','active',0,NULL),(3,'admin@example.com','Admin','active',0,NULL)")
            db.execute(Path(__file__).with_name('schema.sql').read_text(encoding='utf-8'))
        os.environ.update(DATABASE_URL=cls.dsn, AUTH_URL='http://127.0.0.1:8080/api/v1/auth/me', DIRECT_URL='https://direct.example.com', ALLOWED_ORIGINS='https://gateway.example.com,https://alternate.example.com')
        cls.module = importlib.import_module('app')
        cls.client = cls.module.app.test_client()

    def setUp(self):
        with psycopg.connect(self.dsn, autocommit=True) as db:
            db.execute('TRUNCATE direct_access.entries,direct_access.events,direct_access.quota_events,direct_access.quota_overrides,redeem_codes')
            db.execute('UPDATE users SET total_recharged=CASE WHEN id=1 THEN 100 ELSE 0 END')
        self.headers = {'Origin': 'https://gateway.example.com', 'Authorization': 'Bearer synthetic'}

    def test_auth_redirect_never_receives_bearer_token(self):
        received = []

        class RedirectEndpoint(BaseHTTPRequestHandler):
            def do_GET(self):
                received.append((self.path, self.headers.get('Authorization')))
                self.send_response(302)
                self.send_header('Location', '/credential-sink')
                self.end_headers()

            def log_message(self, *_args):
                pass

        server = ThreadingHTTPServer(('127.0.0.1', 0), RedirectEndpoint)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with patch.object(self.module, 'AUTH_URL', f'http://127.0.0.1:{server.server_port}/auth'):
                result = self.client.get('/direct-access/api/entries', headers=self.headers)
            self.assertEqual(result.status_code, 403)
            self.assertEqual(received, [('/auth', 'Bearer synthetic')])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)

    def test_unauthenticated_and_forged_role_denied(self):
        self.assertEqual(self.client.get('/direct-access/api/entries').status_code, 401)
        with patch.object(self.module, 'authenticated_user', return_value={'id': 1, 'role': 'user'}):
            self.assertEqual(self.client.get('/direct-access/api/admin/quotas?q=1', headers={'X-Role': 'admin'}).status_code, 403)
            self.assertEqual(self.client.post('/direct-access/api/admin/quotas/2', json={'limit': 100, 'role': 'admin'}, headers=self.headers).status_code, 403)

    def test_ownership_origin_and_quota(self):
        with patch.object(self.module, 'authenticated_user', return_value={'id': 1, 'role': 'user'}):
            self.assertEqual(self.client.post('/direct-access/api/entries', json={'ip': '9.9.9.9'}, headers={**self.headers, 'Origin': 'https://evil.example'}).status_code, 403)
            created = self.client.post('/direct-access/api/entries', json={'ip': '9.9.9.9'}, headers=self.headers)
            self.assertEqual(created.status_code, 201)
            entry_id = created.get_json()['id']
            self.assertEqual(self.client.post('/direct-access/api/entries', json={'ip': '8.8.8.8'}, headers=self.headers).status_code, 409)
        with patch.object(self.module, 'authenticated_user', return_value={'id': 2, 'role': 'user'}):
            self.assertEqual(self.client.delete('/direct-access/api/entries/' + entry_id, headers=self.headers).status_code, 404)

    def test_admin_override_and_safe_reduction(self):
        with patch.object(self.module, 'authenticated_user', return_value={'id': 1, 'role': 'user'}):
            self.assertEqual(self.client.post('/direct-access/api/entries', json={'ip': '9.9.9.9'}, headers=self.headers).status_code, 201)
        with patch.object(self.module, 'authenticated_user', return_value={'id': 3, 'role': 'admin'}):
            for bad in [True, -1, 101, 1.5]:
                self.assertEqual(self.client.post('/direct-access/api/admin/quotas/1', json={'limit': bad, 'note': 'fixture'}, headers=self.headers).status_code, 400)
            self.assertEqual(self.client.post('/direct-access/api/admin/quotas/1', json={'limit': 0, 'note': 'fixture'}, headers=self.headers).status_code, 409)
            result = self.client.post('/direct-access/api/admin/quotas/1', json={'limit': 7, 'note': 'fixture'}, headers=self.headers)
            self.assertEqual(result.get_json()['limit'], 7)
            result = self.client.post('/direct-access/api/admin/quotas/1', json={'limit': None, 'note': 'reset'}, headers=self.headers)
            self.assertEqual(result.get_json()['limit'], 1)

    def test_credit_history_no_double_count_and_exact_threshold(self):
        with psycopg.connect(self.dsn, autocommit=True) as db:
            db.execute("INSERT INTO redeem_codes VALUES (1,'balance','used',100,now()),(1,'admin_balance','used',99.999999,now()),(1,'admin_balance','unused',500,now()),(1,'admin_balance','used',-20,now())")
            row = db.execute('SELECT total_recharged,ip_limit FROM direct_access.entitlements WHERE user_id=1').fetchone()
            self.assertEqual(str(row[0]), '199.999999')
            self.assertEqual(row[1], 1)
            db.execute("INSERT INTO redeem_codes VALUES (1,'admin_balance','used',0.000001,now())")
            self.assertEqual(db.execute('SELECT ip_limit FROM direct_access.entitlements WHERE user_id=1').fetchone()[0], 2)
            db.execute('UPDATE users SET total_recharged=500 WHERE id=1')
            self.assertEqual(db.execute('SELECT ip_limit FROM direct_access.entitlements WHERE user_id=1').fetchone()[0], 5)

    def test_forwarded_headers_fail_closed(self):
        with self.module.cache_lock:
            self.module.cache.update(ips=frozenset(), loaded=0)
        self.assertEqual(self.client.get('/internal/check', headers={'X-Forwarded-For': '9.9.9.9', 'X-Direct-Peer': '9.9.9.9'}).status_code, 403)

    def test_embedding_redirect_uses_configured_exact_origin(self):
        self.assertEqual(self.client.get('/direct-access/?src_host=https://alternate.example.com', base_url='https://gateway.example.com').status_code, 302)
        result = self.client.get('/direct-access/?src_host=https://evil.example&token=synthetic', base_url='https://gateway.example.com')
        self.assertEqual(result.status_code, 200)
        self.assertEqual(result.headers['Content-Security-Policy'].count("frame-ancestors 'self'"), 1)


if __name__ == '__main__':
    unittest.main()
