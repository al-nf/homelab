'use strict';
const historyState = { device: '__controller__', range: '1h', data: null, error: '', loading: false, request: 0, requestKey: '', fetchedKey: '', fetchedAt: 0 };
const historyMetrics = [
  { key: 'cpu_percent', label: 'CPU usage', unit: '%' },
  { key: 'ram_percent', label: 'Memory usage', unit: '%' },
  { key: 'temperature_c', label: 'CPU temperature', unit: '°C' },
];

function chooseHistory(device, range = historyState.range) {
  if (device === historyState.device && range === historyState.range) return;
  historyState.device = device; historyState.range = range;
  historyState.data = null; historyState.error = ''; historyState.fetchedAt = 0;
  renderHistoryControls(); renderHistoryCharts(); void refreshHistory(true);
}

function renderHistoryControls() {
  const hosts = allHosts();
  const select = $('#history-device');
  const signature = JSON.stringify(hosts.map((host) => [host.id, displayName(host)]));
  if (select.dataset.hosts !== signature) {
    select.replaceChildren();
    for (const host of hosts) {
      const option = element('option', '', displayName(host)); option.value = host.id; select.append(option);
    }
    select.dataset.hosts = signature;
  }
  select.disabled = !hosts.length;
  select.value = historyState.device;
  for (const button of document.querySelectorAll('[data-range]')) button.setAttribute('aria-pressed', String(button.dataset.range === historyState.range));
}

async function refreshHistory(force = false) {
  if (!server) return;
  const key = `${historyState.device}/${historyState.range}`;
  if (historyState.loading && historyState.requestKey === key) return;
  if (!force && key === historyState.fetchedKey && Date.now() - historyState.fetchedAt < 30000) return;
  const request = ++historyState.request;
  historyState.loading = true; historyState.requestKey = key;
  renderHistoryCharts();
  try {
    const data = await api(`/api/hosts/${encodeURIComponent(historyState.device)}/history?range=${historyState.range}`);
    if (request !== historyState.request) return;
    historyState.data = data; historyState.error = data.warning || '';
  } catch (error) {
    if (request !== historyState.request) return;
    historyState.error = historyState.data ? 'Unable to refresh history.' : error.message;
  } finally {
    if (request === historyState.request) {
      historyState.loading = false; historyState.fetchedKey = key; historyState.fetchedAt = Date.now(); renderHistoryCharts();
    }
  }
}

function historySegments(points, key, from, to, maximum) {
  const segments = []; let current = [];
  for (const point of points) {
    const value = point[key]; const time = Date.parse(point.at);
    if (!Number.isFinite(value) || !Number.isFinite(time) || time < from || time > to) {
      if (current.length) segments.push(current); current = []; continue;
    }
    current.push({ x: 36 + 312 * (time - from) / Math.max(1, to - from), y: 108 - 92 * Math.max(0, Math.min(maximum, value)) / maximum, value, time });
  }
  if (current.length) segments.push(current);
  return segments;
}

function historyChart(metric, data) {
  const card = element('article', 'history-chart'); card.dataset.metric = metric.key;
  const heading = element('div', 'history-chart-heading'); heading.append(element('h3', '', metric.label), element('span', '', metric.unit));
  const plot = element('div', 'history-plot');
  const host = allHosts().find((host) => host.id === historyState.device);
  const chart = svg('svg', { viewBox: '0 0 360 142', role: 'img', tabindex: 0, 'data-focus': `history-${metric.key}`, 'aria-label': `${host ? displayName(host) : ''}: ${metric.label}, ${historyState.range === '1h' ? 'last hour' : 'last 24 hours'}. Use left and right arrow keys to inspect samples.` });
  const points = data?.points || [];
  const from = Date.parse(data?.from); const to = Date.parse(data?.to);
  const maximum = Math.max(100, ...points.map((point) => Number.isFinite(point[metric.key]) ? Math.ceil(point[metric.key] / 25) * 25 : 0));
  for (const fraction of [0, 0.5, 1]) {
    const y = 108 - 92 * fraction;
    chart.append(svg('line', { x1: 36, x2: 348, y1: y, y2: y, class: 'history-grid' }), svg('text', { x: 27, y: y + 3, 'text-anchor': 'end', class: 'history-axis' }, String(maximum * fraction)));
  }
  if (Number.isFinite(from) && Number.isFinite(to)) {
    const timeLabel = (at) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    chart.append(svg('text', { x: 36, y: 132, class: 'history-axis' }, timeLabel(from)), svg('text', { x: 348, y: 132, 'text-anchor': 'end', class: 'history-axis' }, timeLabel(to)));
    for (const segment of historySegments(points, metric.key, from, to, maximum)) {
      if (segment.length === 1) {
        chart.append(svg('circle', { cx: segment[0].x, cy: segment[0].y, r: 2.5, class: 'history-dot' })); continue;
      }
      const path = segment.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(' ');
      chart.append(svg('path', { d: `${path} L${segment.at(-1).x.toFixed(2)},108 L${segment[0].x.toFixed(2)},108 Z`, class: 'history-area' }), svg('path', { d: path, class: 'history-line' }));
    }
  }
  const guide = svg('line', { y1: 16, y2: 108, class: 'history-guide', visibility: 'hidden' });
  const marker = svg('circle', { r: 3, class: 'history-dot', visibility: 'hidden' });
  chart.append(guide, marker);
  const tooltip = element('output', 'history-tooltip'); tooltip.hidden = true;
  let inspected = Math.max(0, points.length - 1);
  const inspect = (index) => {
    if (!points.length || !Number.isFinite(from) || !Number.isFinite(to)) return;
    inspected = Math.max(0, Math.min(points.length - 1, index));
    const point = points[inspected]; const time = Date.parse(point.at); const value = point[metric.key];
    const x = 36 + 312 * (time - from) / Math.max(1, to - from);
    guide.setAttribute('x1', x); guide.setAttribute('x2', x); guide.setAttribute('visibility', 'visible');
    marker.setAttribute('visibility', Number.isFinite(value) ? 'visible' : 'hidden');
    if (Number.isFinite(value)) { marker.setAttribute('cx', x); marker.setAttribute('cy', 108 - 92 * Math.max(0, Math.min(maximum, value)) / maximum); }
    tooltip.textContent = `${new Date(time).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })} · ${Number.isFinite(value) ? value.toFixed(1) + metric.unit : 'No data'}`;
    tooltip.hidden = false;
  };
  const clear = () => { guide.setAttribute('visibility', 'hidden'); marker.setAttribute('visibility', 'hidden'); tooltip.hidden = true; };
  chart.addEventListener('pointermove', (event) => {
    const rect = chart.getBoundingClientRect();
    if (!rect.width || !data?.step_seconds) return;
    const fraction = Math.max(0, Math.min(1, ((event.clientX - rect.left) / rect.width * 360 - 36) / 312));
    inspect(Math.round(fraction * (to - from) / (data.step_seconds * 1000)));
  });
  chart.addEventListener('pointerleave', clear);
  chart.addEventListener('focus', () => inspect(inspected)); chart.addEventListener('blur', clear);
  chart.addEventListener('keydown', (event) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault(); inspect(inspected + (event.key === 'ArrowLeft' ? -1 : 1));
  });
  plot.append(chart, tooltip); card.append(heading, plot); return card;
}

function renderHistoryCharts() {
  const container = $('#history-charts');
  const focusKey = document.activeElement?.dataset?.focus;
  container.replaceChildren(...historyMetrics.map((metric) => historyChart(metric, historyState.data)));
  container.setAttribute('aria-busy', String(historyState.loading));
  const status = $('#history-status');
  const hasData = historyState.data?.points?.some((point) => historyMetrics.some((metric) => Number.isFinite(point[metric.key])));
  status.textContent = historyState.error || (historyState.loading && !historyState.data ? 'Loading history…' : !hasData ? 'No history in this range yet.' : '');
  status.hidden = !status.textContent;
  if (focusKey?.startsWith('history-')) for (const node of container.querySelectorAll('[data-focus]')) if (node.dataset.focus === focusKey) { node.focus({ preventScroll: true }); break; }
}

function initHistory() {
  $('#history-device').addEventListener('change', (event) => { selectedHost = event.target.value; chooseHistory(event.target.value); render(); });
  for (const button of document.querySelectorAll('[data-range]')) button.addEventListener('click', () => chooseHistory(historyState.device, button.dataset.range));
  renderHistoryCharts();
}
