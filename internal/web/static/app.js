'use strict';
const $ = (id) => document.getElementById(id);
let selected = '';
let subscriptions = [];
let currentStatus = null;
let gatewayEventHistory = [];
let eventFilter = 'all';
let displayedServerSubscription = '';

function notice(message, isError = false) {
  $('notice').textContent = message;
  $('notice').className = isError ? 'notice error' : 'notice';
}

async function api(path, options = {}) {
  const response = await fetch('/api' + path, {
    ...options,
    headers: {
      ...(options.body ? {'Content-Type':'application/json'} : {}),
      ...(options.headers || {})
    }
  });
  const data = await response.json();
  if (!response.ok) {
    const error = new Error(data.error || `HTTP ${response.status}`);
    error.status = response.status;
    throw error;
  }
  return data;
}

async function action(fn, successText = '') {
  try { await fn(); if (successText) notice(successText); }
  catch(error) { notice(error.message, true); }
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes < 1024) return bytes + ' B';
  const units = ['KB','MB','GB','TB'];
  let n = bytes; let unit = '';
  for (const next of units) { n /= 1024; unit = next; if (n < 1024) break; }
  return n.toFixed(1) + ' ' + unit;
}

function renderSubscriptionMeta() {
  const item = subscriptions.find((sub) => sub.name === selected);
  if (!item) {
    $('subMeta').textContent = 'Нет подписок';
    for (const id of ['activateBtn','updateBtn','deleteBtn']) $(id).disabled = true;
    return;
  }
  for (const id of ['activateBtn','updateBtn','deleteBtn']) $(id).disabled = false;
  $('activateBtn').disabled = item.active;
  const ui = item.userInfo;
  const used = ui ? Number(ui.Upload || 0) + Number(ui.Download || 0) : 0;
  let details = `Серверов: ${item.count}`;
  if (ui && Number(ui.Total) > 0) {
    details += ` · Трафик: ${formatBytes(used)} / ${formatBytes(Number(ui.Total))}`;
  } else if (ui && used > 0) {
    details += ` · Израсходовано: ${formatBytes(used)}`;
  }
  if (item.active) details += ' · Активная подписка';
  if (ui?.Expire && !ui.Expire.startsWith('0001-')) {
    const expiry = new Date(ui.Expire);
    if (!Number.isNaN(expiry.getTime())) details += `\nИстекает: ${expiry.toLocaleDateString('ru-RU')}`;
  }
  $('subMeta').textContent = `${item.title || item.name}\n${details}`;
}

async function loadSubscriptions() {
  subscriptions = await api('/subscriptions');
  if (!subscriptions.some((sub) => sub.name === selected)) {
    selected = subscriptions.find((sub) => sub.active)?.name || subscriptions[0]?.name || '';
  }
  const select = $('subSelect'); select.replaceChildren();
  if (subscriptions.length === 0) {
    const opt = new Option('Нет подписок', ''); select.append(opt);
  } else {
    for (const sub of subscriptions) {
      const opt = new Option(`${sub.name}${sub.active ? ' (активная)' : ''}`, sub.name);
      select.append(opt);
    }
  }
  select.value = selected;
  renderSubscriptionMeta();
  await loadServers();
}

function textNode(tag, className, value) {
  const element = document.createElement(tag);
  element.className = className;
  element.textContent = value;
  return element;
}

async function loadServers() {
  const root = $('servers');
  const selection = selected;
  const scrollTop = selection === displayedServerSubscription ? root.scrollTop : 0;
  if (!selection) {
    root.replaceChildren();
    root.textContent = 'Добавь подписку, чтобы увидеть серверы.';
    displayedServerSubscription = '';
    return;
  }
  const servers = await api('/subscriptions/' + encodeURIComponent(selection) + '/servers');
  if (selection !== selected) return; // Ignore stale responses after switching subscriptions.
  root.replaceChildren();
  displayedServerSubscription = selection;
  if (!servers.length) {root.textContent = 'В этой подписке нет серверов.';return;}
  for (const server of servers) {
    const active = currentStatus?.running && currentStatus.connection?.subscription === selected && currentStatus.connection?.index === server.index;
    const row = document.createElement('div'); row.className = 'serverRow' + (active ? ' connected' : '');
    const description = document.createElement('div');
    description.append(textNode('div','serverTitle',`${server.index}. ${server.tag || server.address}`));
    description.append(textNode('div','serverAddress monospace',formatEndpoint(server.address, server.port)));
    description.append(textNode('div','serverSubtitle',`${server.protocol.toUpperCase()}${server.supported ? '' : ' · Не поддерживается'}`));
    const button = textNode('button',active ? 'secondary' : 'primary',active ? 'Подключено' : 'Подключиться');
    button.type = 'button'; button.disabled = !server.supported || active;
    button.addEventListener('click', () => action(async () => {
      await api('/connect',{method:'POST',body:JSON.stringify({subscription:selected,index:server.index})});
      await loadStatus();await loadServers();
    },'Прокси запущен; проверка доступности выполнится автоматически.'));
    row.append(description,button);root.append(row);
  }
  root.scrollTop = scrollTop;
}

function formatEndpoint(address, port) {
  if (!address) return '';
  const host = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address;
  return port ? `${host}:${port}` : host;
}

function connectionState(status) {
  if (!status.running && !status.autoConnect) return { tone: 'neutral', label: 'Отключено', detail: 'Прокси остановлен' };
  if (!status.running) return { tone: 'error', label: 'Ошибка подключения', detail: 'Не удалось восстановить' };
  if (status.health?.checkedAt && status.health.healthy) {
    return { tone: 'success', label: 'Работает', detail: 'Проверка успешна' };
  }
  if (!status.health?.checkedAt) return { tone: 'warning', label: 'Есть проблемы', detail: 'Проверка соединения' };
  if (status.phase === 'retrying') {
    return { tone: 'error', label: 'Ошибка подключения', detail: 'Не удалось восстановить' };
  }
  return { tone: 'warning', label: 'Есть проблемы', detail: 'Временный сбой' };
}

async function loadStatus() {
  const status = await api('/status');
  const previous = currentStatus;
  currentStatus = status;
  const state = connectionState(status);
  $('connectionBadge').textContent = state.label;
  $('connectionBadge').className = 'badge status-' + state.tone;
  $('healthValue').textContent = state.label;
  $('healthValue').className = 'statValue statusState status-' + state.tone;
  $('healthDetail').textContent = state.detail;
  $('healthDot').className = 'healthDot status-' + state.tone;
  $('currentServer').textContent = status.connection?.tag || 'Выключено';
  $('currentSub').textContent = status.connection
    ? `${status.connection.subscription} · ${status.connection.protocol.toUpperCase()}`
    : 'Нет активного сервера';
  const endpoint = formatEndpoint(status.connection?.address, status.connection?.port);
  $('currentAddress').textContent = endpoint;
  $('currentAddress').hidden = !endpoint;
  const checkedAt = status.health?.checkedAt;
  $('lastCheck').textContent = checkedAt
    ? `Последняя проверка: ${new Date(checkedAt).toLocaleTimeString('ru-RU')}${status.health.error ? ' · Причина: ' + status.health.error : ''}`
    : status.running ? 'Ожидание первой проверки' : 'Нет активного соединения';
  $('exitIP').textContent = status.health?.exitIp || '—';
  $('proxyAddress').textContent = 'SOCKS5 ' + status.proxySocks;
  const phases = {
    connected:'Мониторинг активен', checking:'Проверка подключения',
    refreshing:'Обновление подписки', scanning:'Поиск рабочего сервера',
    retrying:'Ожидание восстановления', degraded:'Обнаружены проблемы',
    disabled:'Отключено', connecting:'Первичная проверка'
  };
  $('recoveryPhase').textContent = phases[status.phase]
    || (status.autoConnect ? 'Мониторинг активен' : 'Отключено');
  const scanning = ['checking', 'refreshing', 'scanning'].includes(status.phase) && status.total > 0;
  const retrying = status.phase === 'retrying' && !!status.retryAt;
  $('recoveryProgressBlock').hidden = !scanning;
  $('recoveryRetryBlock').hidden = !retrying;
  $('recoveryDetails').hidden = !scanning && !retrying;
  if (scanning) $('recoveryProgress').textContent = `${status.checked || 0} из ${status.total}`;
  if (retrying) $('recoveryRetry').textContent = new Date(status.retryAt).toLocaleTimeString('ru-RU');
  if (selected && (previous?.running !== status.running ||
      previous?.connection?.subscription !== status.connection?.subscription ||
      previous?.connection?.index !== status.connection?.index)) {
    await loadServers();
  }
}

function renderGatewayEvents() {
  const root = $('gatewayEvents'); root.replaceChildren();
  const kinds = {
    connection:'Подключение', health:'Проверка соединения',
    refresh:'Обновление подписки', recovery:'Восстановление', probe:'Проверка сервера'
  };
  const levels = {
    success:'Успешно', info:'Информация', warning:'Предупреждение', error:'Ошибка'
  };
  const visible = gatewayEventHistory.filter(event => eventFilter === 'all' || event.level === eventFilter);
  $('eventCount').textContent = `${visible.length} из ${gatewayEventHistory.length}`;
  if (!visible.length) { root.textContent = 'Нет событий для выбранного фильтра'; return; }
  const scrollTop = root.scrollTop;
  for (const event of visible) {
    const item = document.createElement('div');
    const tone = ['success', 'info', 'warning', 'error'].includes(event.level) ? event.level : 'info';
    item.className = 'serverRow eventRow event-' + tone;
    const detail = document.createElement('div');
    detail.append(textNode('div', 'serverTitle', `${new Date(event.time).toLocaleString('ru-RU')} · ${kinds[event.kind] || event.kind}`));
    detail.append(textNode('div', 'serverSubtitle', `${levels[event.level] || event.level}: ${event.message}`));
    item.append(detail); root.append(item);
  }
  root.scrollTop = scrollTop;
}

async function loadEvents() {
  gatewayEventHistory = await api('/events');
  renderGatewayEvents();
}

async function openDashboard() {
  await loadStatus();
  await loadSubscriptions();
  await loadEvents();
  $('loginPanel').hidden = true;
  $('setupPanel').hidden = true;
  $('dashboard').hidden = false;
  $('logoutBtn').hidden = false;
}

$('loginForm').addEventListener('submit',(event) => {event.preventDefault();action(async () => {
  await api('/login', {method:'POST',body:JSON.stringify({username:$('usernameInput').value.trim(),password:$('passwordInput').value})});
  $('passwordInput').value = '';
  await openDashboard();
},'Авторизация успешна.');});

$('logoutBtn').addEventListener('click', () => action(async () => {
  await api('/logout', {method:'POST',body:'{}'});
  $('loginPanel').hidden = false;
  $('dashboard').hidden = true;
  $('logoutBtn').hidden = true;
  currentStatus = null;
  notice('Сессия завершена.');
}));
$('subSelect').addEventListener('change', () => action(async () => {selected=$('subSelect').value;renderSubscriptionMeta();await loadServers();}));
$('refreshBtn').addEventListener('click', () => action(loadSubscriptions,'Список обновлён.'));
$('addForm').addEventListener('submit',(event) => {event.preventDefault();action(async () => {
  const name = $('subName').value.trim();
  await api('/subscriptions',{method:'POST',body:JSON.stringify({name,url:$('subURL').value.trim()})});
  $('subURL').value='';$('subName').value='';selected=name;await loadSubscriptions();
},'Подписка добавлена.');});
$('activateBtn').addEventListener('click',() => action(async () => {await api('/subscriptions/'+encodeURIComponent(selected)+'/activate',{method:'POST'});await loadSubscriptions();},'Активная подписка изменена.'));
$('updateBtn').addEventListener('click',() => action(async () => {
  const path='/subscriptions/'+encodeURIComponent(selected)+'/refresh';
  try{await api(path,{method:'POST',body:'{}'});}catch(error){
    if(error.status!==409 || !confirm('После обновления осталось менее половины серверов. Всё равно заменить кэш?'))throw error;
    await api(path,{method:'POST',body:JSON.stringify({force:true})});
  }
  await loadSubscriptions();
},'Подписка обновлена.'));
$('deleteBtn').addEventListener('click',() => {if(!selected || !confirm(`Удалить подписку «${selected}»?`))return;action(async () => {
  await api('/subscriptions/'+encodeURIComponent(selected),{method:'DELETE'});
  selected='';await loadStatus();await loadSubscriptions();
},'Подписка удалена.');});
$('eventsRefreshBtn').addEventListener('click',()=>action(loadEvents,'История обновлена.'));
$('eventFilter').addEventListener('change', () => {
  eventFilter = $('eventFilter').value;
  renderGatewayEvents();
});
$('disconnectBtn').addEventListener('click',() => action(async () => {await api('/disconnect',{method:'POST'});await loadStatus();await loadServers();},'Прокси остановлен.'));
setInterval(() => {
  if ($('dashboard').hidden) return;
  Promise.all([loadStatus(),loadEvents()]).catch((error) => {
    if (error.status === 401) {
      $('dashboard').hidden = true;
      $('loginPanel').hidden = false;
      $('logoutBtn').hidden = true;
    }
    notice(error.message,true);
  });
},10000);

$('setupForm').addEventListener('submit',(event)=>{event.preventDefault();action(async()=>{
  if ($('setupPassword').value!==$('setupConfirm').value) throw new Error('Пароли не совпадают');
  await api('/setup',{method:'POST',body:JSON.stringify({
    username:$('setupUsername').value.trim(),password:$('setupPassword').value,confirmPassword:$('setupConfirm').value
  })});
  $('setupPassword').value='';$('setupConfirm').value='';
  await openDashboard();
},'Администратор создан.');});
async function initialize() {
  const state=await api('/setup/status');
  if (!state.configured) {
    $('setupPanel').hidden=false;
    $('loginPanel').hidden=true;
    return;
  }
  try {await openDashboard();}
  catch(e){if(e.status===401){$('loginPanel').hidden=false;}else throw e;}
}
initialize().catch((error)=>notice(error.message,true));
