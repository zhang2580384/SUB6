"""Operator-owned startup configuration; no request can change auth routing."""
import ipaddress
import json
import os
from pathlib import Path
from urllib.parse import urlsplit

KEYS = {'DATABASE_URL', 'AUTH_URL', 'DIRECT_URL', 'ALLOWED_ORIGINS',
        'BRAND_NAME', 'PAGE_TITLE', 'PAGE_DESCRIPTION', 'ENTRY_DESCRIPTION'}


def endpoint(value, *, auth=False, origin=False):
    if not isinstance(value, str) or len(value) > 2048 or any(c.isspace() for c in value) or '\\' in value or '*' in value:
        raise ValueError('Endpoint must be an absolute URL without whitespace')
    parsed = urlsplit(value)
    try:
        parsed.port
        loopback = ipaddress.ip_address(parsed.hostname or '').is_loopback
    except ValueError:
        loopback = False
        # Invalid ports must not be mistaken for valid hostnames.
        parsed.port
    if not parsed.hostname or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment:
        raise ValueError('Endpoint must not contain credentials, query or fragment')
    if parsed.scheme != 'https' and not (auth and parsed.scheme == 'http' and loopback):
        raise ValueError('Use HTTPS, or a loopback HTTP auth service')
    if auth and parsed.path != '/api/v1/auth/me':
        raise ValueError('AUTH_URL must use the fixed /api/v1/auth/me path')
    if origin and parsed.path:
        raise ValueError('ALLOWED_ORIGINS must contain exact origins without paths')
    return value


def load_config(environ=None):
    env = os.environ if environ is None else environ
    values = {}
    path = env.get('DIRECT_ACCESS_CONFIG_FILE')
    if path:
        values = json.loads(Path(path).read_text(encoding='utf-8'))
        if not isinstance(values, dict) or set(values) - KEYS:
            raise ValueError('Unknown direct-access configuration key')
    values.update({k: env[k] for k in KEYS if k in env})
    for required in ('DATABASE_URL', 'AUTH_URL', 'DIRECT_URL', 'ALLOWED_ORIGINS'):
        if not isinstance(values.get(required), str) or not values[required].strip():
            raise ValueError(f'{required} is required; no production default is supplied')
    endpoint(values['AUTH_URL'], auth=True)
    endpoint(values['DIRECT_URL'])
    origins = [x.strip() for x in values['ALLOWED_ORIGINS'].split(',')]
    if not 1 <= len(origins) <= 16:
        raise ValueError('Configure between 1 and 16 exact site origins')
    for origin in origins:
        endpoint(origin, origin=True)
    values['ORIGINS'] = frozenset(origins)
    defaults = {'BRAND_NAME': 'SUB6', 'PAGE_TITLE': 'IP 白名单直连',
                'PAGE_DESCRIPTION': '为固定出口的服务器提供直连接口，适合下游中转和大请求上传。',
                'ENTRY_DESCRIPTION': '沿用现有 API Key、分组权限及计费。兼容 HTTP / SSE / WebSocket。'}
    for key, default in defaults.items():
        value = values.get(key, default)
        if not isinstance(value, str) or not value.strip() or len(value) > 300:
            raise ValueError(f'{key} must be 1 to 300 characters of plain text')
        values[key] = value.strip()
    values['CONFIG_SOURCE'] = 'file + environment' if path else 'environment'
    return values
