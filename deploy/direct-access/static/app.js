'use strict';
const qs = new URLSearchParams(location.search);
// The server validates configured origins before serving this document.
if (qs.get('theme') === 'dark') document.documentElement.classList.add('dark');
initialize();

function initialize() {
  const $ = id => document.getElementById(id);
  let refreshError = false;
  let currentIP = '', directURL = '', pendingDelete = null, loading = false, failures = 0, selectedUser = null;
  function message(text, error = false) {
    $('message').textContent = text; $('message').hidden = !text;
    $('message').className = 'message' + (error ? ' error' : '');
  }
  async function api(path = '', options = {}, resource = 'entries') {
    const token = localStorage.getItem('auth_token');
    if (!token) throw new Error('请先登录网站，再打开「IP 白名单直连」页面。');
    const response = await fetch('/direct-access/api/' + resource + path, {
      ...options, headers: {...options.headers, 'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}, cache: 'no-store', credentials: 'omit'
    });
    if (response.status === 204) return null;
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || '请求失败，请稍后重试');
    return data;
  }
  function cell(row, text, cls) {
    const td = document.createElement('td'); td.textContent = text; if (cls) td.className = cls; row.append(td); return td;
  }
  async function refresh(manual = false) {
    if (loading) return; loading = true;
    try {
      const data = await api(); failures = 0;
      currentIP = data.current_ip; directURL = data.direct_url;
      $('endpoint').textContent = directURL; $('copy').disabled = false;
      $('current').textContent = '当前浏览器出口：' + (currentIP || '未检测到');
      $('quota').textContent = data.entries.length + ' / ' + data.limit + ' 已用';
      $('recharge').textContent = '计入名额的累计充值：$' + Number(data.total_recharged).toLocaleString('en-US', {minimumFractionDigits: 2, maximumFractionDigits: 2}) + '（含管理员加款 $' + Number(data.admin_credit_total).toLocaleString('en-US', {minimumFractionDigits: 2, maximumFractionDigits: 2}) + '） · 当前总名额 ' + data.limit + ' 个' + (data.manual_limit === null ? '（自动计算）' : '（管理员指定）');
      $('admin-panel').hidden = !data.is_admin;
      $('submit').disabled = data.entries.length >= data.limit;
      $('submit').textContent = data.limit === 0 ? (data.manual_limit === null ? '累计充值满 $100 后可添加' : '当前名额为 0，请联系管理员') : '添加到白名单';
      const body = $('entries'); body.replaceChildren();
      if (!data.entries.length) {
        const tr = document.createElement('tr'), td = cell(tr, '还没有白名单 IP。添加服务器的固定公网出口后，即可使用直连。', 'empty'); td.colSpan = 5; body.append(tr);
      }
      for (const entry of data.entries) {
        const tr = document.createElement('tr'); cell(tr, entry.ip); cell(tr, entry.label || '—');
        const status = cell(tr, ''), badge = document.createElement('span');
        badge.className = 'status ' + entry.status; badge.textContent = entry.status === 'active' ? '已生效' : entry.status === 'over_quota' ? '名额不足' : '同步中'; status.append(badge);
        cell(tr, new Date(entry.created_at).toLocaleString('zh-CN', {hour12: false}));
        const actions = cell(tr, '', 'right'), button = document.createElement('button');
        button.className = 'delete'; button.textContent = '移除'; button.setAttribute('aria-label', '移除 ' + entry.ip);
        button.onclick = () => { pendingDelete = entry.id; $('delete-detail').textContent = entry.ip + (entry.label ? ' · ' + entry.label : ''); $('delete-dialog').showModal(); };
        actions.append(button); body.append(tr);
      }
      if (!data.sync_ok) {
        message('白名单正在同步，请稍后刷新；长时间未生效请联系管理员。', true); refreshError = true;
      } else {
        if (manual || refreshError) message(manual ? '状态已更新' : '');
        refreshError = false;
      }
    } catch (error) {
      failures++; refreshError = true; message(error.message, true); $('submit').disabled = true;
      if (failures === 1) $('entries').replaceChildren();
    } finally { loading = false; }
  }
  $('add-form').onsubmit = async event => {
    event.preventDefault(); $('submit').disabled = true;
    try {
      await api('', {method: 'POST', body: JSON.stringify({ip: $('ip').value.trim(), label: $('label').value.trim()})});
      $('add-form').reset(); message('已提交，约 30 秒内生效。'); await refresh();
    } catch (error) { message(error.message, true); $('submit').disabled = false; }
  };
  $('fill').onclick = () => { if (currentIP) $('ip').value = currentIP; else message('未检测到当前 IP，请手动填写。', true); };
  $('copy').onclick = async () => {
    try { await navigator.clipboard.writeText(directURL); message('直连地址已复制'); }
    catch { message('请手动复制上方直连地址。'); }
  };
  $('refresh').onclick = () => refresh(true);
  $('cancel-delete').onclick = () => $('delete-dialog').close();
  $('confirm-delete').onclick = async () => {
    $('confirm-delete').disabled = true;
    try { await api('/' + pendingDelete, {method: 'DELETE'}); $('delete-dialog').close(); message('已移除，约 30 秒内同步完成。'); await refresh(); }
    catch (error) { message(error.message, true); }
    finally { $('confirm-delete').disabled = false; }
  };
  function selectUser(user) {
    selectedUser = user;
    $('admin-quota-form').hidden = false;
    $('admin-selected').textContent = '调整客户：' + user.email + '（ID ' + user.user_id + '）';
    $('admin-detail').textContent = '当前总名额 ' + user.ip_limit + ' 个 · 已登记 ' + user.used + ' 个 · 计入名额的累计充值 $' + Number(user.total_recharged).toFixed(2) + '（含管理员加款 $' + Number(user.admin_credit_total).toFixed(2) + '） · ' + (user.manual_limit === null ? '按累计充值自动计算' : '管理员指定') + (user.note ? ' · 上次备注：' + user.note : '');
    $('admin-limit').value = user.ip_limit;
    $('admin-note').value = '';
  }
  async function searchUsers() {
    const query = $('admin-query').value.trim();
    $('admin-search').disabled = true;
    $('admin-message').textContent = '正在查询…';
    $('admin-quota-form').hidden = true; selectedUser = null;
    $('admin-results').replaceChildren();
    try {
      const data = await api('?q=' + encodeURIComponent(query), {}, 'admin/quotas');
      $('admin-message').textContent = data.users.length ? '找到 ' + data.users.length + ' 位客户（最多显示 20 位），请核对邮箱后选择。' : '未找到客户，请核对邮箱、用户名或 ID。';
      for (const user of data.users) {
        const tr = document.createElement('tr');
        cell(tr, user.email + (user.username ? ' · ' + user.username : '') + '（ID ' + user.user_id + '）');
        cell(tr, '$' + Number(user.total_recharged).toFixed(2) + '（手动加款 $' + Number(user.admin_credit_total).toFixed(2) + '）'); cell(tr, user.auto_limit + ' 个');
        cell(tr, user.ip_limit + ' 个 · ' + (user.manual_limit === null ? '自动' : '指定')); cell(tr, user.used + ' 个');
        const button = document.createElement('button'); button.className = 'secondary'; button.textContent = '调整名额';
        button.setAttribute('aria-label', '调整 ' + user.email + ' 的名额');
        button.onclick = () => selectUser(user); cell(tr, '').append(button); $('admin-results').append(tr);
      }
    } catch (error) { $('admin-message').textContent = error.message; }
    finally { $('admin-search').disabled = false; }
  }
  $('admin-search-form').onsubmit = event => { event.preventDefault(); searchUsers(); };
  async function saveQuota(reset) {
    if (!selectedUser) return;
    if (!$('admin-note').reportValidity() || (!reset && !$('admin-limit').reportValidity())) return;
    const user = selectedUser;
    const limit = reset ? null : Number($('admin-limit').value);
    $('admin-save').disabled = true; $('admin-reset').disabled = true;
    try {
      const data = await api('/' + user.user_id, {method: 'POST', body: JSON.stringify({limit, note: $('admin-note').value.trim()})}, 'admin/quotas');
      await searchUsers(); await refresh();
      $('admin-message').textContent = '已为 ' + user.email + (reset ? ' 恢复自动计算' : ' 设置指定名额') + '，当前总名额 ' + data.limit + ' 个。客户刷新页面即可看到；放行约 30 秒内同步。';
    } catch (error) { $('admin-message').textContent = error.message; }
    finally { $('admin-save').disabled = false; $('admin-reset').disabled = false; }
  }
  $('admin-quota-form').onsubmit = event => { event.preventDefault(); saveQuota(false); };
  $('admin-reset').onclick = () => saveQuota(true);
  refresh();
  setInterval(() => { if (!document.hidden && failures < 5) refresh(); }, 10000);
}
