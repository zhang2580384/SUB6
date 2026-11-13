"""Loopback-only management service. Main Sub2API remains the auth authority."""
import ipaddress
import json
import logging
import os
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path

from flask import Flask, abort, jsonify, redirect, request, send_from_directory
from psycopg.rows import dict_row
from psycopg_pool import ConnectionPool
from werkzeug.exceptions import HTTPException

from policy import public_ip

app = Flask(__name__, static_folder=None)
app.config['MAX_CONTENT_LENGTH'] = 4096
pool = ConnectionPool(os.environ['DATABASE_URL'], min_size=1, max_size=5,
                      timeout=5, kwargs={'row_factory': dict_row, 'connect_timeout': 5,
                      'options': '-c statement_timeout=5000'})
AUTH_URL = os.environ['AUTH_URL']
DIRECT_URL = os.environ['DIRECT_URL']
ORIGINS = set(filter(None, (v.strip() for v in os.environ['ALLOWED_ORIGINS'].split(','))))
ROOT = Path(__file__).parent
class RejectAuthRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward the customer's bearer token to a redirect destination.
        return None


opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), RejectAuthRedirect())
cache_lock = threading.Lock()
cache = {'ips': frozenset(), 'loaded': 0.0}


@app.after_request
def security_headers(response):
    response.headers['Cache-Control'] = 'private, no-store'
    response.headers['X-Content-Type-Options'] = 'nosniff'
    response.headers['Referrer-Policy'] = 'no-referrer'
    response.headers['Content-Security-Policy'] = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"
    return response


@app.errorhandler(HTTPException)
def http_error(error):
    return jsonify(error=error.description), error.code


@app.errorhandler(Exception)
def internal_error(error):
    logging.exception('direct-access request failed')
    return jsonify(error='服务暂时不可用，请稍后重试'), 503


def authenticated_user():
    auth = request.headers.get('Authorization', '')
    if not auth.startswith('Bearer ') or len(auth) > 8192:
        abort(401, '请先登录网站后使用')
    req = urllib.request.Request(AUTH_URL, headers={'Authorization': auth})
    try:
        with opener.open(req, timeout=6) as response:
            data = json.loads(response.read(65536))
        user = data['data']
        uid = int(user['id'])
        if uid <= 0 or user.get('status', 'active') != 'active':
            abort(403, '账号当前不可用')
        return user
    except urllib.error.HTTPError as e:
        abort(401 if e.code == 401 else 403, '登录已失效，请刷新网站重新登录')
    except (urllib.error.URLError, TimeoutError, KeyError, ValueError):
        abort(503, '登录校验暂不可用，请稍后重试')


def identity():
    return int(authenticated_user()['id'])


def administrator():
    user = authenticated_user()
    if user.get('role') != 'admin':
        abort(403, '仅管理员可管理客户白名单名额')
    return int(user['id'])


def mutate_guard():
    if request.headers.get('Origin') not in ORIGINS:
        abort(403, '请从本站白名单页面操作')
    if request.method == 'POST' and not request.is_json:
        abort(415, '请使用 JSON 请求')


def transaction_lock(conn, uid):
    # Serialize create/delete/quota checks, including concurrent requests and workers.
    conn.execute('SELECT pg_advisory_xact_lock(8142001)')
    n = conn.execute("SELECT count(*) n FROM direct_access.events WHERE user_id=%s AND created_at>now()-interval '1 minute'", (uid,)).fetchone()['n']
    if n >= 10:
        abort(429, '操作过于频繁，请一分钟后重试')


def state_rows(conn):
    return conn.execute('SELECT ips,applied_at FROM direct_access.sync_state WHERE id=1').fetchone()


def entitlement(conn, uid):
    row = conn.execute('SELECT total_recharged,ip_limit,auto_limit,manual_limit,system_total_recharged,redeem_credit_total,admin_credit_total FROM direct_access.entitlements WHERE user_id=%s', (uid,)).fetchone()
    if not row:
        abort(403, '账号当前不可用')
    return row


def load_allowed():
    with pool.connection() as conn:
        state = state_rows(conn)
        age = (datetime.now(timezone.utc) - state['applied_at']).total_seconds()
        rows = conn.execute('SELECT host(ip) ip FROM direct_access.eligible_entries').fetchall()
        # A dead synchronizer or failed database cannot grant access indefinitely.
        ips = frozenset(r['ip'] for r in rows) & frozenset(state['ips']) if age < 45 else frozenset()
    with cache_lock:
        cache.update(ips=ips, loaded=time.monotonic())


def cache_loop():
    while True:
        try:
            load_allowed()
        except Exception:
            logging.exception('allowlist refresh failed')
        time.sleep(3)


try:
    load_allowed()
except Exception:
    logging.exception('initial allowlist refresh failed; direct access stays closed')
threading.Thread(target=cache_loop, daemon=True).start()


@app.get('/internal/check')
def check():
    # Caddy overwrites this field with the TCP peer; never use X-Forwarded-For.
    try:
        ip = str(ipaddress.ip_address(request.headers.get('X-Direct-Peer', '')))
    except ValueError:
        abort(403, 'DIRECT_IP_NOT_ALLOWED')
    with cache_lock:
        allowed = ip in cache['ips'] and time.monotonic() - cache['loaded'] < 30
    if not allowed:
        abort(403, 'DIRECT_IP_NOT_ALLOWED: 请在网站添加实际出口 IP 并等待生效')
    return '', 204


@app.get('/direct-access/')
def page():
    # A cross-origin iframe can be blocked by frame-ancestors before its JS runs.
    # Redirect before serving HTML, so storage/API calls use the parent's origin.
    source = request.args.get('src_host', '')
    if source in ORIGINS and urllib.parse.urlsplit(source).netloc != request.host:
        theme = 'dark' if request.args.get('theme') == 'dark' else 'light'
        return redirect(source + '/direct-access/?' + urllib.parse.urlencode({'theme': theme}), code=302)
    return send_from_directory(ROOT / 'static', 'index.html')


@app.get('/direct-access/<any(app.js,style.css):name>')
def assets(name):
    return send_from_directory(ROOT / 'static', name)


@app.get('/direct-access/api/entries')
def listing():
    user = authenticated_user()
    uid = int(user['id'])
    with pool.connection() as conn:
        rows = conn.execute('SELECT id,host(ip) ip,label,created_at FROM direct_access.entries WHERE user_id=%s AND deleted_at IS NULL ORDER BY created_at', (uid,)).fetchall()
        state = state_rows(conn)
        quota = entitlement(conn, uid)
        eligible = {r['id'] for r in conn.execute('SELECT id FROM direct_access.eligible_entries WHERE user_id=%s', (uid,)).fetchall()}
    synced = (datetime.now(timezone.utc)-state['applied_at']).total_seconds() < 45
    with cache_lock:
        enabled = cache['ips'] if time.monotonic()-cache['loaded'] < 30 else frozenset()
    for r in rows:
        eligible_entry = r['id'] in eligible
        r['id'] = str(r['id'])
        r['created_at'] = r['created_at'].isoformat()
        r['status'] = 'over_quota' if not eligible_entry else ('active' if synced and r['ip'] in enabled else 'pending')
    return jsonify(entries=rows, limit=quota['ip_limit'], total_recharged=str(quota['total_recharged']),
                   admin_credit_total=str(quota['admin_credit_total']),
                   redeem_credit_total=str(quota['redeem_credit_total']),
                   system_total_recharged=str(quota['system_total_recharged']),
                   auto_limit=quota['auto_limit'], manual_limit=quota['manual_limit'],
                   is_admin=user.get('role') == 'admin',
                   quota_step=100, quota_cap=100, direct_url=DIRECT_URL,
                   current_ip=request.headers.get('X-Verified-Client-IP', ''),
                   sync_ok=synced)


@app.post('/direct-access/api/entries')
def create():
    mutate_guard()
    uid = identity()
    body = request.get_json()
    if not isinstance(body, dict):
        abort(400, '请求格式不正确')
    try:
        ip = public_ip(body.get('ip'))
    except ValueError as e:
        abort(400, str(e))
    label = body.get('label', '')
    if not isinstance(label, str) or len(label.strip()) > 60:
        abort(400, '备注最多 60 个字符')
    entry_id = uuid.uuid4()
    with pool.connection() as conn:
        transaction_lock(conn, uid)
        quota = entitlement(conn, uid)
        if conn.execute('SELECT 1 FROM direct_access.entries WHERE user_id=%s AND ip=%s::inet AND deleted_at IS NULL', (uid, ip)).fetchone():
            abort(409, '此 IP 已在你的白名单中')
        count = conn.execute('SELECT count(*) n FROM direct_access.entries WHERE user_id=%s AND deleted_at IS NULL', (uid,)).fetchone()['n']
        if count >= quota['ip_limit']:
            abort(409, '白名单名额不足：请查看当前可用总名额。自动规则为累计充值每满 $100 获得 1 个名额，管理员可单独指定。迁移时请勿先删除仍在使用的旧 IP；需要更多名额请联系管理员。')
        total = conn.execute('SELECT count(*) n FROM direct_access.entries WHERE deleted_at IS NULL').fetchone()['n']
        if total >= 5000:
            abort(503, '白名单暂时已满，请联系管理员')
        conn.execute('INSERT INTO direct_access.entries(id,user_id,ip,label) VALUES(%s,%s,%s,%s)', (entry_id, uid, ip, label.strip()))
        conn.execute("INSERT INTO direct_access.events(user_id,action,entry_id,ip) VALUES(%s,'create',%s,%s)", (uid, entry_id, ip))
    return jsonify(id=str(entry_id), status='pending'), 201


@app.delete('/direct-access/api/entries/<uuid:entry_id>')
def delete(entry_id):
    mutate_guard()
    uid = identity()
    with pool.connection() as conn:
        transaction_lock(conn, uid)
        row = conn.execute('UPDATE direct_access.entries SET deleted_at=now() WHERE id=%s AND user_id=%s AND deleted_at IS NULL RETURNING ip', (entry_id, uid)).fetchone()
        if not row:
            abort(404, '记录不存在')
        conn.execute("INSERT INTO direct_access.events(user_id,action,entry_id,ip) VALUES(%s,'delete',%s,%s)", (uid, entry_id, row['ip']))
    return '', 204


@app.get('/direct-access/api/admin/quotas')
def admin_quotas():
    administrator()
    query = request.args.get('q', '').strip()
    if not 2 <= len(query) <= 100 and not (query.isascii() and query.isdigit() and len(query) == 1):
        abort(400, '请输入用户 ID，或至少两个字符的邮箱／用户名')
    uid = int(query) if query.isascii() and query.isdigit() and len(query) <= 18 else -1
    with pool.connection() as conn:
        rows = conn.execute('''SELECT q.*,
            (SELECT count(*) FROM direct_access.entries e WHERE e.user_id=q.user_id AND e.deleted_at IS NULL) used
            FROM direct_access.quota_admin_users q
            WHERE q.user_id=%s OR strpos(lower(q.email),lower(%s))>0 OR strpos(lower(coalesce(q.username,'')),lower(%s))>0
            ORDER BY (q.user_id=%s) DESC,q.user_id LIMIT 20''', (uid, query, query, uid)).fetchall()
    for row in rows:
        for field in ('total_recharged','system_total_recharged','redeem_credit_total','admin_credit_total'):
            row[field] = str(row[field])
        row['updated_at'] = row['updated_at'].isoformat() if row['updated_at'] else None
    return jsonify(users=rows)


@app.post('/direct-access/api/admin/quotas/<int:uid>')
def admin_set_quota(uid):
    mutate_guard()
    actor = administrator()
    body = request.get_json()
    if not isinstance(body, dict) or 'limit' not in body:
        abort(400, '请指定总名额；恢复自动规则请传 null')
    limit = body['limit']
    if limit is not None and (type(limit) is not int or not 0 <= limit <= 100):
        abort(400, '总名额必须是 0 到 100 的整数')
    note = body.get('note', '')
    if not isinstance(note, str) or not 1 <= len(note.strip()) <= 200:
        abort(400, '请填写 1 到 200 字的调整备注')
    if not 0 < uid <= 9223372036854775807:
        abort(400, '用户 ID 不正确')
    with pool.connection() as conn:
        conn.execute('SELECT pg_advisory_xact_lock(8142001)')
        n = conn.execute("SELECT count(*) n FROM direct_access.quota_events WHERE actor_id=%s AND created_at>now()-interval '1 minute'", (actor,)).fetchone()['n']
        if n >= 10:
            abort(429, '调整过于频繁，请一分钟后重试')
        row = conn.execute('SELECT * FROM direct_access.entitlements WHERE user_id=%s', (uid,)).fetchone()
        if not row:
            abort(404, '客户不存在')
        effective = row['auto_limit'] if limit is None else limit
        used = conn.execute('SELECT count(*) n FROM direct_access.entries WHERE user_id=%s AND deleted_at IS NULL', (uid,)).fetchone()['n']
        if effective < used:
            abort(409, f'客户已登记 {used} 个 IP，不能降至 {effective} 个。请先与客户确认并移除不再使用的 IP，避免业务中断。')
        conn.execute('''INSERT INTO direct_access.quota_overrides(user_id,ip_limit,note,updated_by)
            VALUES(%s,%s,%s,%s) ON CONFLICT(user_id) DO UPDATE
            SET ip_limit=EXCLUDED.ip_limit,note=EXCLUDED.note,updated_by=EXCLUDED.updated_by,updated_at=now()''',
            (uid, limit, note.strip(), actor))
        conn.execute('INSERT INTO direct_access.quota_events(actor_id,user_id,old_limit,new_limit,note) VALUES(%s,%s,%s,%s,%s)',
            (actor, uid, row['manual_limit'], limit, note.strip()))
    return jsonify(user_id=uid, limit=effective, manual_limit=limit, auto_limit=row['auto_limit'])
