'use strict';
const $ = (id) => document.getElementById(id);
let selected = '';
let subscriptions = [];
let currentStatus = null;

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
  const traffic = ui && Number(ui.Total) > 0 ? `${formatBytes(used)} / ${formatBytes(Number(ui.Total))}` : 'нет информации';
  const expire = ui && ui.Expire && !ui.Expire.startsWith('0001-') ? new Date(ui.Expire).toLocaleDateString('ru-RU') : 'неизвестно';
  $('subMeta').textContent = `${item.title || item.name}\nСерверов: ${item.count} · Трафик: ${traffic}\nИстекает: ${expire}${item.active ? ' · Активная подписка' : ''}`;
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
  const root = $('servers');root.replaceChildren();
  if (!selected) {root.textContent = 'Добавь подписку, чтобы увидеть серверы.';return;}
  const servers = await api('/subscriptions/' + encodeURIComponent(selected) + '/servers');
  if (!servers.length) {root.textContent = 'В этой подписке нет серверов.';return;}
  for (const server of servers) {
    const active = currentStatus?.running && currentStatus.connection?.subscription === selected && currentStatus.connection?.index === server.index;
    const row = document.createElement('div'); row.className = 'serverRow' + (active ? ' connected' : '');
    const description = document.createElement('div');
    description.append(textNode('div','serverTitle',`${server.index}. ${server.tag || server.address}`));
    description.append(textNode('div','serverSubtitle',`${server.protocol.toUpperCase()} · ${server.address}:${server.port}${server.supported ? '' : ' · Не поддерживается'}`));
    const button = textNode('button',active ? 'secondary' : 'primary',active ? 'Подключено' : 'Подключиться');
    button.type = 'button'; button.disabled = !server.supported || active;
    button.addEventListener('click', () => action(async () => {
      await api('/connect',{method:'POST',body:JSON.stringify({subscription:selected,index:server.index})});
      await loadStatus();await loadServers();
    },'Прокси запущен; проверка доступности выполнится автоматически.'));
    row.append(description,button);root.append(row);
  }
}

async function loadStatus() {
  const status = await api('/status');
  currentStatus = status;
  $('connectionBadge').textContent = status.running ? 'Прокси запущен' : 'Отключено';
  $('connectionBadge').className = status.running ? 'badge running' : 'badge';
  $('currentServer').textContent = status.connection?.tag || 'Выключено';
  $('currentSub').textContent = status.connection ? `${status.connection.subscription} · ${status.connection.protocol.toUpperCase()}` : 'Нет активного сервера';
  $('healthValue').textContent = !status.running ? '—' : !status.health.checkedAt ? 'Ожидание проверки' : status.health.healthy ? 'Работает' : 'Ошибка проверки';
  $('lastCheck').textContent = status.health.checkedAt ? `Проверено ${new Date(status.health.checkedAt).toLocaleTimeString('ru-RU')}${status.health.error ? ' · ' + status.health.error : ''}` : 'Нет измерений';
  $('exitIP').textContent = status.health.exitIp || '—';
  $('proxyAddress').textContent = 'SOCKS5 ' + status.proxySocks;
}

async function openDashboard() {
  await loadStatus();
  await loadSubscriptions();
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
$('disconnectBtn').addEventListener('click',() => action(async () => {await api('/disconnect',{method:'POST'});await loadStatus();await loadServers();},'Прокси остановлен.'));
setInterval(() => {
  if ($('dashboard').hidden) return;
  loadStatus().catch((error) => {
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
    username:$('setupUsername').value.trim(),password:$('setupPassword').value,
    migrationToken:$('migrationToken').value
  })});
  $('setupPassword').value='';$('setupConfirm').value='';$('migrationToken').value='';
  await openDashboard();
},'Администратор создан.');});
async function initialize() {
  const state=await api('/setup/status');
  if (!state.configured) {
    $('setupPanel').hidden=false;
    $('loginPanel').hidden=true;
    $('migrationField').hidden=!state.migrationRequired;
    return;
  }
  try {await openDashboard();}
  catch(e){if(e.status===401){$('loginPanel').hidden=false;}else throw e;}
}
initialize().catch((error)=>notice(error.message,true));
