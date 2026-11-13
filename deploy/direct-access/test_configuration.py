"""Pure configuration checks; no application database or network needed."""
import json
from pathlib import Path
import tempfile
import unittest

from configuration import load_config


class ConfigurationTests(unittest.TestCase):
    def env(self, **changes):
        return {'DATABASE_URL': 'postgresql://fixture@127.0.0.1/sub6_review',
                'AUTH_URL': 'https://gateway.example.com/api/v1/auth/me',
                'DIRECT_URL': 'https://direct.example.com',
                'ALLOWED_ORIGINS': 'https://gateway.example.com', **changes}

    def test_required_no_private_defaults(self):
        for key in ('DATABASE_URL', 'AUTH_URL', 'DIRECT_URL', 'ALLOWED_ORIGINS'):
            env = self.env()
            del env[key]
            with self.subTest(key=key), self.assertRaises(ValueError):
                load_config(env)

    def test_fixed_auth_path_and_exact_https_origins(self):
        for bad in ('http://10.0.0.10/api/v1/auth/me',
                    'https://gateway.example.com/arbitrary-path',
                    'https://user:secret@gateway.example.com/api/v1/auth/me',
                    'https://gateway.example.com/api/v1/auth/me?next=https://evil.example',
                    'https://gateway.example.com/api/v1/auth/me#part',
                    'file:///api/v1/auth/me', 'https://gateway.example.com:bad/api/v1/auth/me'):
            with self.subTest(url=bad), self.assertRaises(ValueError):
                load_config(self.env(AUTH_URL=bad))
        load_config(self.env(AUTH_URL='http://127.0.0.1:8080/api/v1/auth/me'))
        for bad in ('*', 'https://*.example.com', 'http://gateway.example.com',
                    'https://gateway.example.com/path', 'https://gateway.example.com,'):
            with self.subTest(origin=bad), self.assertRaises(ValueError):
                load_config(self.env(ALLOWED_ORIGINS=bad))

    def test_operator_file_environment_precedence_and_unknown_key_rejection(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'settings.json'
            p.write_text(json.dumps(self.env(BRAND_NAME='File brand')), encoding='utf-8')
            config = load_config({'DIRECT_ACCESS_CONFIG_FILE': str(p), 'BRAND_NAME': 'Operator brand'})
            self.assertEqual(config['BRAND_NAME'], 'Operator brand')
            self.assertEqual(config['CONFIG_SOURCE'], 'file + environment')
            p.write_text('{"unexpected_setting": true}', encoding='utf-8')
            with self.assertRaises(ValueError):
                load_config({'DIRECT_ACCESS_CONFIG_FILE': str(p)})


if __name__ == '__main__':
    unittest.main()
