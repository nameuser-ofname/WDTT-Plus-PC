'use strict';
// ---------- утилиты ----------
const $root = document.getElementById('root');
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(kid));
  return el;
}
const fmtBytes = n => { n = n || 0; const u = ['Б','КБ','МБ','ГБ','ТБ']; let i = 0; while (n >= 1024 && i < 4) { n /= 1024; i++; } return (i ? n.toFixed(n < 10 ? 2 : 1) : n) + ' ' + u[i]; };
const fmtDate = ts => ts ? new Date(ts * 1000).toLocaleString('ru-RU') : '—';
const STATUS = { active: 'активен', deactivated: 'отключён', expired: 'истёк', expired_retained: 'истёк (хранится)', broken: 'повреждён' };

async function req(method, path, body) {
  const res = await fetch(path, {
    method, credentials: 'same-origin',
    headers: { 'X-Requested-With': 'wdtt-admin', 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await res.json(); } catch {}
  if (res.status === 401 && path !== '/api/login') { state.authed = false; render(); throw new Error('Сессия истекла'); }
  if (!res.ok || data.ok === false) throw new Error(data.message || data.code || ('HTTP ' + res.status));
  return data;
}
const admin = (cmd, flags) => req('POST', '/api/admin', { cmd, flags: flags || {} });

let toastTimer;
function toast(msg, err) {
  document.querySelectorAll('.toast').forEach(t => t.remove());
  const t = h('div', { class: 'toast' + (err ? ' err' : '') }, msg);
  document.body.append(t);
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.remove(), err ? 7000 : 3500);
}
async function act(fn, okMsg) {
  try { const r = await fn(); if (okMsg !== false) toast(okMsg || r.message || 'Готово'); return r; }
  catch (e) { toast(e.message, true); return null; }
}
function modal(title, body, buttons) {
  const close = () => ov.remove();
  const ov = h('div', { class: 'overlay', onmousedown: e => { if (e.target === ov) close(); } },
    h('div', { class: 'modal' }, h('h3', {}, title), body, h('div', { class: 'row-actions mt' }, buttons(close))));
  document.body.append(ov);
  return close;
}
function confirmBox(text, label, fn, word) {
  const inp = word ? h('input', { placeholder: 'Введите: ' + word }) : null;
  modal('Подтверждение', h('div', {}, h('p', {}, text), inp), close => [
    h('button', { class: 'btn danger', onclick: async () => {
      if (word && inp.value !== word) return toast('Нужно ввести «' + word + '»', true);
      close(); await fn();
    } }, label),
    h('button', { class: 'btn sec', onclick: close }, 'Отмена'),
  ]);
}
function field(label, input) { return h('div', {}, h('label', {}, label), input); }
async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast('Скопировано'); }
  catch { prompt('Скопируйте вручную:', text); }
}
function download(name, text) {
  const a = h('a', { href: URL.createObjectURL(new Blob([text], { type: 'application/json' })), download: name });
  document.body.append(a); a.click(); a.remove();
}

// ---------- состояние ----------
const state = { authed: null, tab: 'overview', server: null, clients: [], filter: '', svc: null, backups: null, logs: '' };

function clientLink(c) {
  const host = (state.server && (state.server.effective_public_ip || state.server.public_ip)) || location.hostname;
  const p = (c.ports || state.server.default_ports || '').split(',');
  return `wdtt://${host}:${p[0]}:${p[1]}:${p[2]}:${c.password}:${c.vk_hash || ''}`;
}

async function refresh() {
  const r = await req('POST', '/api/admin', { cmd: 'list' });
  state.server = r.server; state.clients = r.passwords || [];
}

// ---------- вход ----------
function loginView() {
  const pw = h('input', { type: 'password', placeholder: 'Пароль панели', autofocus: true });
  const go = async () => { if (await act(() => req('POST', '/api/login', { password: pw.value }), false)) { state.authed = true; await boot(); } };
  pw.addEventListener('keydown', e => e.key === 'Enter' && go());
  return h('div', { class: 'login card' }, h('h2', {}, 'WDTT Admin'), field('Пароль', pw), h('div', { class: 'mt' }, h('button', { class: 'btn', onclick: go }, 'Войти')));
}

// ---------- вкладки ----------
function overviewView() {
  const s = state.server;
  if (!s) return h('div', { class: 'muted' }, 'Загрузка…');
  const st = (v, l) => h('div', { class: 'stat' }, h('b', {}, v), h('span', {}, l));
  const t = s.traffic || {};
  return h('div', {},
    h('div', { class: 'card' }, h('h2', {}, 'Сервер'), h('div', { class: 'grid' },
      st(s.active_connections ?? 0, 'активных соединений'),
      st((s.cpu_percent || 0).toFixed(1) + '%', 'CPU'),
      st((s.memory_percent || 0).toFixed(1) + '%', 'память'),
      st((s.network_rx_mbps || 0).toFixed(1) + ' / ' + (s.network_tx_mbps || 0).toFixed(1), 'сеть RX / TX, Мбит/с'),
      st(`${s.active_password_count}/${s.max_passwords}`, 'клиентов (активных/лимит)'),
      st(s.device_count, 'устройств'),
      st(s.packet_drops, 'потеряно пакетов'),
      st(s.reconnects, 'переподключений'))),
    h('div', { class: 'card' }, h('h2', {}, 'Трафик клиентов'), h('div', { class: 'grid' },
      ...[['today', 'Сегодня'], ['week', '7 дней'], ['month', '30 дней'], ['all', 'Всего']].map(([k, l]) =>
        st('↓ ' + fmtBytes(t[k]?.down) + '  ↑ ' + fmtBytes(t[k]?.up), l)))),
    h('div', { class: 'card' }, h('h2', {}, 'Информация'),
      h('div', { class: 'mono' }, `Версия: ${s.wdtt_plus_version}  ·  WG: ${s.wg_backend || '—'}  ·  Публичный адрес: ${s.effective_public_ip || s.public_ip || '—'}  ·  Порты по умолчанию: ${s.default_ports}  ·  DNS: ${s.dns || '—'}`)));
}

function clientsView() {
  const q = state.filter.toLowerCase();
  const list = state.clients.filter(c => !q || [c.label, c.password, c.device_name, c.vk_hash].some(x => (x || '').toLowerCase().includes(q)));
  const search = h('input', { placeholder: 'Поиск…', value: state.filter });
  search.addEventListener('input', () => { state.filter = search.value; const pos = search.selectionStart; render(); const n = document.querySelector('.toolbar input'); n.focus(); n.setSelectionRange(pos, pos); });
  return h('div', { class: 'card' },
    h('div', { class: 'toolbar' }, search, h('button', { class: 'btn', onclick: createClientModal }, '+ Новый клиент'),
      h('button', { class: 'btn sec', onclick: async () => { await act(refresh, false); render(); } }, 'Обновить')),
    h('div', { class: 'tablewrap' }, h('table', {},
      h('thead', {}, h('tr', {}, ...['Название', 'Статус', 'Действует до', 'Трафик 30д', 'Устройство', ''].map(x => h('th', {}, x)))),
      h('tbody', {}, list.length ? list.map(c => h('tr', { class: 'row', onclick: () => clientModal(c) },
        h('td', {}, c.label || h('span', { class: 'muted mono' }, c.password.slice(0, 10) + '…')),
        h('td', {}, h('span', { class: 'badge ' + c.status }, STATUS[c.status] || c.status)),
        h('td', {}, c.expires_at ? fmtDate(c.expires_at) : 'бессрочно'),
        h('td', {}, '↓ ' + fmtBytes(c.traffic?.month?.down) + ' ↑ ' + fmtBytes(c.traffic?.month?.up)),
        h('td', {}, c.device_name ? `${c.device_name} (${c.device_ip || '—'})` : h('span', { class: 'muted' }, 'не привязано')),
        h('td', {}, h('button', { class: 'btn sec', onclick: e => { e.stopPropagation(); copy(clientLink(c)); } }, 'Ссылка'))))
        : h('tr', {}, h('td', { colspan: 6, class: 'muted' }, 'Клиентов нет'))))));
}

async function reload() { await refresh(); render(); }

function createClientModal() {
  const label = h('input', { placeholder: 'Например: Вася' }), hash = h('input', { placeholder: 'ссылка vk.com/call/join/… или хеш' }),
    days = h('input', { type: 'number', min: 0, max: 365, value: 30 }), ports = h('input', { placeholder: 'по умолчанию: ' + (state.server?.default_ports || '') }),
    pass = h('input', { placeholder: 'пусто = сгенерировать' }), off = h('input', { type: 'checkbox' });
  modal('Новый клиент', h('div', {}, field('Название', label), field('VK-хеш', hash),
    h('div', { class: 'two' }, field('Срок, дней (0 = бессрочно)', days), field('Порты DTLS,WG,TUN', ports)),
    field('Пароль клиента', pass), h('label', {}, off, ' создать отключённым')), close => [
    h('button', { class: 'btn', onclick: async () => {
      const f = { label: label.value, 'vk-hash': hash.value, days: String(days.value || 0) };
      if (ports.value.trim()) f.ports = ports.value.trim();
      if (pass.value.trim()) f['client-password'] = pass.value.trim();
      if (off.checked) f.deactivated = 'true';
      const r = await act(() => admin('create', f), 'Клиент создан');
      if (r) { close(); await reload(); const c = r.password; if (c) copy(clientLink(c)); }
    } }, 'Создать'),
    h('button', { class: 'btn sec', onclick: close }, 'Отмена')]);
}

function clientModal(c) {
  const label = h('input', { value: c.label || '' }), hash = h('input', { value: c.vk_hash || '' }), ports = h('input', { value: c.ports || '' });
  const days = h('input', { type: 'number', min: 0, max: 365, value: 30 });
  const info = h('div', { class: 'mono muted pre' }, `Пароль: ${c.password}\nПоследняя активность: ${fmtDate(c.device_last_seen)}\nТрафик всего: ↓ ${fmtBytes(c.down_bytes)} ↑ ${fmtBytes(c.up_bytes)}\nПривязок устройства: ${c.bind_events_count || 0}`);
  const run = (fn, msg, closeIt) => async () => { const r = await act(fn, msg); if (r) { closeIt(); await reload(); } };
  modal(c.label || 'Клиент', h('div', {}, info, field('Название', label), field('VK-хеш', hash), field('Порты DTLS,WG,TUN', ports),
    field('Продлить: новый срок от сегодня, дней (0 = бессрочно)', h('div', { class: 'inline' }, days))), close => [
    h('button', { class: 'btn', onclick: run(() => admin('update-client', { password: c.password, label: label.value, 'vk-hash': hash.value, ports: ports.value }), 'Сохранено', close) }, 'Сохранить'),
    h('button', { class: 'btn sec', onclick: run(() => admin('set-expiry', { password: c.password, days: String(days.value || 0) }), 'Срок обновлён', close) }, 'Применить срок'),
    h('button', { class: 'btn sec', onclick: () => copy(clientLink(c)) }, 'Копировать ссылку'),
    h('button', { class: 'btn sec', onclick: run(() => admin(c.status === 'deactivated' ? 'activate' : 'deactivate', { password: c.password }), null, close) }, c.status === 'deactivated' ? 'Включить' : 'Отключить'),
    c.device_id && h('button', { class: 'btn sec', onclick: run(() => admin('unbind', { password: c.password }), 'Устройство отвязано', close) }, 'Отвязать устройство'),
    h('button', { class: 'btn sec', onclick: () => { const np = prompt('Новый пароль клиента (ссылка изменится):'); if (np) run(() => admin('set-password', { password: c.password, 'new-password': np }), 'Пароль изменён', close)(); } }, 'Сменить пароль'),
    h('button', { class: 'btn danger', onclick: () => confirmBox('Удалить клиента «' + (c.label || c.password) + '»? Это необратимо.', 'Удалить', run(() => admin('delete', { password: c.password }), 'Удалён', close)) }, 'Удалить'),
    h('button', { class: 'btn sec', onclick: close }, 'Закрыть')]);
}

function settingsView() {
  const s = state.server || {};
  const dns = h('input', { value: s.dns || '' }), limit = h('input', { type: 'number', value: s.max_passwords }),
    ports = h('input', { value: s.default_ports || '' }), pub = h('input', { value: s.public_ip || '', placeholder: 'auto' });
  const save = (cmd, v) => async () => { if (await act(() => admin(cmd, { value: v().trim() }))) await reload(); };
  const row = (l, i, cmd, v) => field(l, h('div', { class: 'inline' }, i, h('button', { class: 'btn', onclick: save(cmd, v) }, 'Сохранить')));
  return h('div', {},
    h('div', { class: 'card' }, h('h2', {}, 'Параметры сервера'),
      row('DNS для клиентов (через запятую)', dns, 'set-dns', () => dns.value),
      row('Максимум клиентов', limit, 'set-limit', () => String(limit.value)),
      row('Порты по умолчанию для ссылок (DTLS,WG,TUN)', ports, 'set-default-ports', () => ports.value),
      row('Публичный адрес в ссылках (домен/IP или auto)', pub, 'set-public-ip', () => pub.value || 'auto'),
      h('p', { class: 'muted' }, 'Эти порты попадают только в новые ссылки; реальные порты задаются в systemd-юните wdtt.service.')),
    h('div', { class: 'card' }, h('h2', {}, 'Обслуживание'), h('div', { class: 'row-actions' },
      h('button', { class: 'btn sec', onclick: async () => { if (await act(() => admin('refresh-public-ip'))) await reload(); } }, 'Определить публичный IP'),
      h('button', { class: 'btn sec', onclick: async () => { if (await act(() => admin('cleanup-expired'))) await reload(); } }, 'Удалить истёкших клиентов'),
      h('button', { class: 'btn sec', onclick: async () => { if (await act(() => admin('cleanup-orphans'))) await reload(); } }, 'Удалить «осиротевшие» устройства'),
      h('button', { class: 'btn danger', onclick: () => confirmBox('Сбросить статистику трафика всех клиентов?', 'Сбросить', async () => { if (await act(() => admin('reset-traffic'))) await reload(); }) }, 'Сбросить трафик'))));
}

async function loadBackups() { const r = await act(() => admin('backup-status'), false); if (r) { state.backups = r.backup_status; render(); } }
function backupsView() {
  const b = state.backups;
  if (!b) return h('div', { class: 'muted' }, 'Загрузка…');
  const en = h('input', { type: 'checkbox' }); en.checked = !!b.policy.enabled;
  const iv = h('input', { type: 'number', min: 1, value: b.policy.interval_hours }), keep = h('input', { type: 'number', min: 1, value: b.policy.retention_count });
  return h('div', {},
    h('div', { class: 'card' }, h('h2', {}, 'Автоматические копии'), h('div', { class: 'two' }, field('Интервал, часов', iv), field('Хранить копий', keep)),
      h('label', {}, en, ' включить'), h('div', { class: 'row-actions mt' },
        h('button', { class: 'btn', onclick: async () => { await act(() => admin('backup-configure', { enabled: String(en.checked), 'interval-hours': String(iv.value), retention: String(keep.value) })); loadBackups(); } }, 'Сохранить'),
        h('button', { class: 'btn sec', onclick: async () => { await act(() => admin('backup-create', { reason: 'manual' }), 'Копия создана'); loadBackups(); } }, 'Создать копию сейчас'))),
    h('div', { class: 'card' }, h('h2', {}, `Копии (${b.valid_count} исправных, ${b.broken_count} повреждённых, ${fmtBytes(b.total_bytes)})`),
      h('div', { class: 'tablewrap' }, h('table', {}, h('thead', {}, h('tr', {}, ...['Дата', 'Причина', 'Клиентов', 'Размер', 'Статус', ''].map(x => h('th', {}, x)))),
        h('tbody', {}, (b.backups || []).length ? b.backups.map(x => h('tr', {},
          h('td', {}, fmtDate(x.created_at)), h('td', {}, x.reason), h('td', {}, x.password_count), h('td', {}, fmtBytes(x.size_bytes)),
          h('td', {}, x.valid ? h('span', { class: 'badge active' }, 'исправна') : h('span', { class: 'badge broken' }, x.error || 'ошибка')),
          h('td', {}, h('div', { class: 'row-actions' },
            h('button', { class: 'btn sec', onclick: async () => { await act(() => admin('backup-verify', { id: x.id }), 'Копия исправна'); } }, 'Проверить'),
            h('button', { class: 'btn sec', onclick: async () => { const r = await act(() => admin('backup-export', { id: x.id }), false); if (r) download('wdtt-backup-' + x.id + '.json', JSON.stringify(r.backup_document, null, 2)); } }, 'Скачать'),
            h('button', { class: 'btn danger', onclick: () => confirmBox('Удалить копию?', 'Удалить', async () => { await act(() => admin('backup-delete', { id: x.id, confirm: 'УДАЛИТЬ' })); loadBackups(); }) }, 'Удалить')))))
          : h('tr', {}, h('td', { colspan: 6, class: 'muted' }, 'Копий пока нет')))))));
}

async function loadService() { const r = await act(() => req('GET', '/api/service'), false); if (r) { state.svc = r; render(); } }
async function loadLogs() { const r = await act(() => req('GET', '/api/logs?lines=300'), false); if (r) { state.logs = r.logs; render(); const p = document.querySelector('pre.log'); if (p) p.scrollTop = p.scrollHeight; } }
function serviceView() {
  const s = state.svc;
  const svc = a => async () => { await act(() => req('POST', '/api/service', { action: a })); setTimeout(loadService, 1500); };
  return h('div', {},
    h('div', { class: 'card' }, h('h2', {}, 'Служба wdtt.service'),
      s ? h('p', {}, 'Состояние: ', h('span', { class: 'badge ' + (s.active === 'active' ? 'active' : 'expired') }, s.active), '  автозапуск: ' + s.enabled + '  ·  запущена: ' + (s.since || '—')) : h('p', { class: 'muted' }, 'Загрузка…'),
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn', onclick: () => confirmBox('Перезапустить WDTT? Активные клиенты переподключатся.', 'Перезапустить', svc('restart')) }, 'Перезапустить'),
        h('button', { class: 'btn sec', onclick: svc('start') }, 'Запустить'),
        h('button', { class: 'btn danger', onclick: () => confirmBox('Остановить WDTT? Все клиенты отключатся.', 'Остановить', svc('stop')) }, 'Остановить'),
        h('button', { class: 'btn sec', onclick: () => { loadService(); loadLogs(); } }, 'Обновить'))),
    h('div', { class: 'card' }, h('h2', {}, 'Журнал'), h('pre', { class: 'log' }, state.logs || 'Нажмите «Обновить»')));
}

function accountView() {
  const cur = h('input', { type: 'password' }), nw = h('input', { type: 'password' });
  return h('div', { class: 'card narrow' }, h('h2', {}, 'Пароль панели'),
    field('Текущий пароль', cur), field('Новый пароль (от 8 символов)', nw),
    h('div', { class: 'mt' }, h('button', { class: 'btn', onclick: async () => {
      if (await act(() => req('POST', '/api/panel-password', { current: cur.value, new: nw.value }))) { state.authed = false; render(); }
    } }, 'Сменить')));
}

const TABS = [['overview', 'Обзор', overviewView], ['clients', 'Клиенты', clientsView], ['settings', 'Настройки', settingsView],
  ['backups', 'Копии', backupsView], ['service', 'Служба', serviceView], ['account', 'Аккаунт', accountView]];

function render() {
  const scroll = window.scrollY;
  $root.replaceChildren();
  if (state.authed === null) return;
  if (!state.authed) { $root.append(loginView()); return; }
  const view = TABS.find(t => t[0] === state.tab)[2];
  $root.append(h('div', { class: 'wrap' },
    h('header', { class: 'top' }, h('h1', {}, 'WDTT Admin'),
      h('nav', {}, TABS.map(([id, label]) => h('button', { class: state.tab === id ? 'on' : '', onclick: () => go(id) }, label))),
      h('button', { class: 'btn sec', onclick: async () => { await act(() => req('POST', '/api/logout'), false); state.authed = false; render(); } }, 'Выйти')),
    view()));
  window.scrollTo(0, scroll);
}

function go(tab) {
  state.tab = tab; render();
  if (tab === 'backups') loadBackups();
  if (tab === 'service') { loadService(); loadLogs(); }
}

async function boot() {
  await act(refresh, false);
  render();
}

setInterval(async () => {
  if (!state.authed || document.hidden || document.querySelector('.overlay')) return;
  if (state.tab === 'overview' || state.tab === 'clients') { try { await refresh(); if (!document.querySelector('.overlay') && document.activeElement?.tagName !== 'INPUT') render(); } catch {} }
}, 10000);

(async () => {
  try { await req('GET', '/api/session'); state.authed = true; await boot(); }
  catch { state.authed = false; render(); }
})();
