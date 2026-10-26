'use strict';
const $ = (selector) => document.querySelector(selector);
let authenticated = false;
let loginAvailable = false;
let machines = [];
let connectionLost = false;
let toastTimer;
let selectedHost = null;
let selectedLink = null;
let server = null;
const waking = new Set();
const dialog = $('#login-dialog');
async function api(path, options = {}) {
  const response = await fetch(path, { signal: AbortSignal.timeout(10000), ...options, headers: { 'Content-Type': 'application/json', 'X-Homelab-Request': '1' } });
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 401) { authenticated = false; render(); }
    throw new Error(data.error || 'The request failed. Please try again.');
  }
  return data;
}
function toast(message, error = false) {
  const node = $('#toast'); node.textContent = message; node.dataset.error = error; node.hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => { node.hidden = true; }, 7000);
}
function login() {
  if (!loginAvailable) { toast('Login is not configured on this server.', true); return; }
  $('#login-error').textContent = ''; $('#password').value = ''; dialog.showModal(); $('#password').focus();
}
function element(tag, className, text) {
  const node = document.createElement(tag); node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function svg(tag, attributes = {}, text) {
  const node = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, value);
  if (text !== undefined) node.textContent = text;
  return node;
}
function fresh(host) { return !connectionLost && !host.stale && Boolean(host.telemetry?.updated_at); }
function stateLabel(host) { return connectionLost ? 'Connection lost' : fresh(host) ? 'Reporting' : host.telemetry?.updated_at ? 'Stale telemetry' : 'Awaiting collector'; }
function speedLabel(host) {
  if (!fresh(host)) return host.telemetry?.updated_at ? 'No recent data' : 'Awaiting link data';
  const link = host.telemetry?.link;
  if (link?.carrier === false) return 'Link down';
  if (link?.type === 'tailscale') return 'Tailscale';
  const speed = link?.speed_mbps;
  if (speed == null) return 'Speed unavailable';
  return speed >= 1000 ? `${Number((speed / 1000).toFixed(2))} Gbps` : `${speed} Mbps`;
}
function displayName(host) { return (host.name || host.id).toLowerCase(); }
function linkType(host) {
  return ({ ethernet: 'Ethernet', wifi: 'Wi-Fi', tailscale: 'Tailscale', other: 'Network' })[host.telemetry?.link?.type] || 'Ethernet';
}
function hubName() { return server?.hostname?.toLowerCase() || '…'; }
function controllerHost() {
  return { id: '__controller__', name: hubName(), is_controller: true, telemetry: server?.telemetry || {}, stale: server?.telemetry_stale !== false };
}
function allHosts() { return server ? [controllerHost(), ...machines] : machines; }
function durationLabel(seconds) {
  if (!Number.isFinite(seconds)) return '—';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor(seconds / 3600) % 24;
  const minutes = Math.floor(seconds / 60) % 60;
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : minutes ? `${minutes}m` : `${Math.floor(seconds)}s`;
}
function rateLabel(bps) {
  if (!Number.isFinite(bps)) return 'Awaiting next sample';
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(2)} Gbps`;
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(2)} Mbps`;
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} Kbps`;
  return `${Math.round(bps)} bps`;
}
function totalLabel(bytes) {
  if (!Number.isFinite(bytes)) return 'Unavailable';
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(2)} GiB`;
  if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MiB`;
  return `${(bytes / 1024).toFixed(1)} KiB`;
}
function renderDeployment() {
  if (!server) return;
  $('#server-hostname').textContent = hubName();
  const started = new Date(server.started_at);
  $('#server-started').textContent = Number.isNaN(started.getTime()) ? '—' : started.toLocaleString();
  $('#server-started').dateTime = server.started_at;
  $('#server-uptime').textContent = connectionLost ? 'Connection lost' : durationLabel(server.uptime_seconds);
}
function renderLinkDetails() {
  const panel = $('#link-details');
  const host = machines.find((machine) => machine.id === selectedLink);
  panel.hidden = !host;
  if (!host) return;
  const t = host.telemetry || {};
  const link = t.link;
  const stale = !fresh(host);
  const live = !stale && link?.carrier !== false;
  $('#link-details-type').textContent = `${linkType(host).toUpperCase()} CONNECTION`;
  $('#link-details-title').textContent = `${hubName()} ↔ ${displayName(host)}`;
  const speed = link?.speed_mbps;
  const negotiated = link?.type === 'tailscale' ? 'Virtual link' : Number.isFinite(speed) ? (speed >= 1000 ? `${Number((speed / 1000).toFixed(2))} Gbps` : `${speed} Mbps`) + (stale ? ' (last reported)' : '') : 'Unavailable';
  const values = [
    ['Connection type', linkType(host)],
    ['Negotiated speed', negotiated],
    [`Interface on ${displayName(host)}`, link?.interface || 'Unavailable'],
    [`Receive at ${displayName(host)}`, live ? rateLabel(link?.rx_bps) : 'Unavailable'],
    [`Transmit from ${displayName(host)}`, live ? rateLabel(link?.tx_bps) : 'Unavailable'],
    ['Counter scope', 'Interface totals (all traffic)'],
    ['Received total', totalLabel(link?.rx_bytes)],
    ['Transmitted total', totalLabel(link?.tx_bytes)],
    ['Carrier', link?.carrier == null ? 'Unknown' : `${stale ? 'Last reported ' : ''}${link.carrier ? 'up' : 'down'}`],
    ['Last sample', t.updated_at ? new Date(t.updated_at).toLocaleString() + (stale ? ' · stale' : '') : 'No samples received'],
  ];
  const list = $('#link-details-values'); list.replaceChildren();
  for (const [label, value] of values) {
    const pair = element('div', ''); pair.append(element('dt', '', label), element('dd', '', value)); list.append(pair);
  }
}
function renderGraph() {
  const graph = $('#network-graph'); graph.replaceChildren();
  const count = machines.length;
  const height = count <= 5 ? 320 : Math.max(400, count * 60);
  const cy = height / 2;
  graph.setAttribute('viewBox', `0 0 900 ${height}`);
  graph.append(svg('title', {}, `Network connections to ${hubName()}`));
  const links = svg('g'); const nodes = svg('g');
  machines.forEach((host, index) => {
    const angle = count <= 2 ? index * Math.PI : (index * Math.PI * 2 / count) - Math.PI / 2;
    const x = 450 + Math.cos(angle) * 300;
    const y = cy + Math.sin(angle) * (height / 2 - 70);
    const active = fresh(host) && host.telemetry?.link?.carrier !== false;
    const label = speedLabel(host);
    const line = svg('line', { x1: 450, y1: cy, x2: x, y2: y, class: `graph-edge ${active ? 'live' : 'stale'}` });
    line.append(svg('title', {}, `${displayName(host)} ↔ ${hubName()}: ${label}`));
    const edge = svg('g', { class: `graph-link ${selectedLink === host.id ? 'selected' : ''}`, role: 'button', tabindex: 0, 'aria-label': `Connection ${hubName()} to ${displayName(host)}: ${label}. Show details.`, 'data-focus': `link-${host.id}` });
    const selectEdge = () => { selectedLink = host.id; render(); };
    edge.addEventListener('click', selectEdge);
    edge.addEventListener('keydown', (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectEdge(); } });
    edge.append(svg('line', { x1: 450, y1: cy, x2: x, y2: y, class: 'edge-hit-area' }), line);
    const mx = (450 + x) / 2; const my = (cy + y) / 2;
    const labelWidth = Math.max(76, label.length * 6.5 + 20);
    edge.append(svg('rect', { x: mx - labelWidth / 2, y: my - 12, width: labelWidth, height: 24, rx: 5, class: 'edge-label-bg' }), svg('text', { x: mx, y: my + 4, class: `edge-label ${active ? 'live' : ''}` }, label));
    links.append(edge);
    const group = svg('g', { class: `graph-host ${fresh(host) ? 'live' : 'stale'} ${selectedHost === host.id ? 'selected' : ''}`, role: 'button', tabindex: 0, 'aria-label': `${displayName(host)}, ${stateLabel(host)}, ${label}`, 'data-focus': `graph-${host.id}` });
    group.append(svg('title', {}, `${displayName(host)}: ${stateLabel(host)}`), svg('rect', { x: x - 67, y: y - 29, width: 134, height: 58, rx: 10 }), svg('circle', { cx: x - 45, cy: y, r: 3, class: 'node-indicator' }), svg('text', { x: x + 3, y: y + 4, class: 'node-name' }, displayName(host).length > 16 ? displayName(host).slice(0, 15) + '…' : displayName(host)));
    const select = () => { selectedHost = host.id; chooseHistory(host.id); render(); document.getElementById(`host-${host.id}`)?.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'nearest' }); };
    group.addEventListener('click', select);
    group.addEventListener('keydown', (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); select(); } });
    nodes.append(group);
  });
  const hub = svg('g', { class: `graph-hub ${selectedHost === '__controller__' ? 'selected' : ''}`, role: 'button', tabindex: 0, 'aria-label': `${hubName()}, management server. Show telemetry.`, 'data-focus': 'graph-__controller__' });
  const selectHub = () => {
    selectedHost = '__controller__'; chooseHistory('__controller__'); render();
    document.getElementById('host-__controller__')?.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'nearest' });
  };
  hub.addEventListener('click', selectHub);
  hub.addEventListener('keydown', (event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectHub(); } });
  hub.append(svg('circle', { cx: 450, cy, r: 51, class: 'hub-halo' }), svg('circle', { cx: 450, cy, r: 41, class: 'hub-body' }), svg('text', { x: 450, y: cy + 1, class: 'hub-name' }, hubName().length > 16 ? hubName().slice(0, 15) + '…' : hubName()), svg('text', { x: 450, y: cy + 17, class: 'hub-detail' }, 'CONTROL'));
  graph.append(links, hub, nodes);
}
function addMetric(container, label, number, unit, stale) {
  const metric = element('div', 'metric');
  const valid = Number.isFinite(number);
  const value = element('div', 'metric-value' + (valid && !stale ? ' measured' : ''), valid ? number.toFixed(1) : '—');
  value.append(element('small', '', unit));
  const track = element('div', 'metric-track');
  if (valid && (unit === '%' || unit === '°C') && !stale) {
    const bar = document.createElement('progress'); bar.max = 100; bar.value = Math.max(0, Math.min(100, number)); bar.setAttribute('aria-label', label);
    if (unit === '°C') {
      bar.className = 'heat';
      // Keep the gradient anchored to the full 0–100°C scale as the fill grows.
      bar.style.setProperty('--heat-size', `${10000 / Math.max(0.01, bar.value)}%`);
      bar.setAttribute('aria-valuetext', `${number.toFixed(1)}°C`);
    }
    track.append(bar);
  }
  metric.append(element('span', 'metric-label', label), value, track); container.append(metric);
}
function memoryLabel(used, total) {
  return Number.isFinite(used) && Number.isFinite(total) ? `${(used / 1073741824).toFixed(1)} / ${(total / 1073741824).toFixed(1)} GiB` : 'Unavailable';
}
function render() {
  const hosts = allHosts();
  const focusKey = document.activeElement?.dataset?.focus;
  $('#auth-button').textContent = authenticated ? 'Log out ↗' : 'Log in ↗';
  $('#access-label').textContent = authenticated ? 'Control authorized' : 'Public view';
  $('#host-count').textContent = hosts.length;
  $('#machine-count').textContent = String(hosts.length).padStart(2, '0');
  const reporting = hosts.filter(fresh).length;
  $('#telemetry-summary').textContent = connectionLost ? 'Disconnected' : reporting ? `${reporting} / ${hosts.length} reporting` : 'Awaiting data';
  renderDeployment(); renderGraph(); renderLinkDetails(); renderHistoryControls();
  const container = $('#hosts'); container.replaceChildren(); container.setAttribute('aria-busy', 'false');
  for (const host of hosts) {
    const t = host.telemetry || {}; const stale = !fresh(host);
    const card = element('article', 'host-card' + (selectedHost === host.id ? ' selected' : ''));
    card.id = `host-${host.id}`;
    const heading = element('div', 'host-heading'); const identity = element('div', 'host-identity');
    const icon = element('div', 'server-icon'); icon.setAttribute('aria-hidden', 'true');
    icon.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.3"><rect x="4" y="3" width="16" height="7" rx="2"/><rect x="4" y="14" width="16" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" stroke-linecap="round"/></svg>';
    const name = element('div', ''); name.append(element('h3', '', displayName(host)));
    identity.append(icon, name); heading.append(identity, element('span', 'pill' + (!stale ? ' reporting' : ''), stateLabel(host)));
    const metrics = element('div', 'metrics');
    addMetric(metrics, 'CPU temperature', t.temperature_c, '°C', stale);
    addMetric(metrics, 'CPU usage', t.cpu_percent, '%', stale);
    addMetric(metrics, 'Memory usage', t.ram_percent, '%', stale);
    card.append(heading, metrics);
    const detail = element('div', 'reading-detail');
    detail.append(element('span', '', `RAM · ${memoryLabel(t.ram_used_bytes, t.ram_total_bytes)}`));
    if (stale && t.updated_at) { const time = document.createElement('time'); time.dateTime = t.updated_at; time.textContent = `Last received ${new Date(t.updated_at).toLocaleString()}`; detail.append(time); }
    else if (stale) detail.append(element('span', '', 'No samples received'));
    card.append(detail);
    for (const gpu of t.gpus || []) {
      const section = element('div', 'gpu-section'); section.append(element('h4', '', gpu.name || 'GPU'));
      const gpuMetrics = element('div', 'metrics');
      addMetric(gpuMetrics, 'GPU temperature', gpu.temperature_c, '°C', stale);
      addMetric(gpuMetrics, 'GPU usage', gpu.utilization_percent, '%', stale);
      addMetric(gpuMetrics, 'VRAM usage', gpu.vram_total_bytes ? 100 * gpu.vram_used_bytes / gpu.vram_total_bytes : null, '%', stale);
      section.append(gpuMetrics, element('p', 'gpu-memory', memoryLabel(gpu.vram_used_bytes, gpu.vram_total_bytes))); card.append(section);
    }
    if (host.is_controller || host.can_wake === false) { container.append(card); continue; }
    const button = element('button', 'button wake-button ' + (authenticated ? 'primary' : 'secondary'), waking.has(host.id) ? 'Sending…' : 'Wake ↗');
    button.disabled = !authenticated || waking.has(host.id); button.dataset.focus = `wake-${host.id}`;
    button.setAttribute('aria-label', (authenticated ? 'Wake ' : 'Log in to wake ') + displayName(host));
    button.title = authenticated ? '' : 'Log in to wake';
    button.addEventListener('click', () => { if (authenticated) wake(host); }); card.append(button); container.append(card);
  }
  if (focusKey) for (const node of document.querySelectorAll('[data-focus]')) if (node.dataset.focus === focusKey) { node.focus({ preventScroll: true }); break; }
}
async function wake(host) {
  waking.add(host.id); render();
  try { const result = await api('/api/hosts/' + encodeURIComponent(host.id) + '/wake', { method: 'POST' }); toast(result.message); }
  catch (error) { toast(error.message, true); }
  finally { waking.delete(host.id); render(); }
}
$('#auth-button').addEventListener('click', async () => {
  if (!authenticated) { login(); return; }
  try { await api('/api/logout', { method: 'POST' }); authenticated = false; render(); } catch (error) { toast(error.message, true); }
});
$('#close-link-details').addEventListener('click', () => {
  const previous = selectedLink; selectedLink = null; render();
  for (const node of document.querySelectorAll('[data-focus]')) if (node.dataset.focus === `link-${previous}`) { node.focus({ preventScroll: true }); break; }
});
$('#close-dialog').addEventListener('click', () => dialog.close());
dialog.addEventListener('close', () => { $('#password').value = ''; });
$('#login-form').addEventListener('submit', async (event) => {
  event.preventDefault(); $('#login-submit').disabled = true; $('#login-error').textContent = '';
  try {
    await api('/api/login', { method: 'POST', body: JSON.stringify({ password: $('#password').value }) });
    authenticated = true; dialog.close(); render(); toast('Logged in. Wake controls are unlocked.');
  } catch (error) { $('#login-error').textContent = error.message; }
  finally { $('#login-submit').disabled = false; }
});
async function load() {
  try {
    const [hosts, session, status] = await Promise.all([api('/api/hosts'), api('/api/session'), api('/api/health')]);
    server = status; machines = hosts; authenticated = session.authenticated; loginAvailable = session.login_available; connectionLost = false; render(); void refreshHistory();
  } catch (error) {
    if (!connectionLost) toast('Unable to refresh the dashboard. Retrying…', true);
    connectionLost = true; render();
  } finally { setTimeout(load, 5000); }
}
initHistory();
load();
