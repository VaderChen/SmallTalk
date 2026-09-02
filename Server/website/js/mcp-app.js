(function () {
  'use strict';
  const $ = id => document.getElementById(id);
  const client = new SmallTalkMCPClient();
  const projectID = decodeURIComponent(document.cookie.split(';').map(v => v.trim()).find(v => v.startsWith('smalltalk_project='))?.split('=').slice(1).join('=') || 'default');
  let selectedRoom = null;
  let lastMessageID = '';

  function show(value) { $('output').textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2); }
  function status(text, error = false) { $('status').textContent = text; $('status').className = error ? 'error' : 'status'; }
  function args(extra = {}) { return { project_id: projectID, room_id: selectedRoom, ...extra }; }
  async function run(fn) { try { status('MCP 操作中...'); const value = await fn(); show(value); status('MCP 操作完成'); return value; } catch (e) { status(e.message || String(e), true); } }

  async function loadRooms() {
    const rooms = await client.call('smalltalk_list_rooms', { project_id: projectID });
    const list = Array.isArray(rooms) ? rooms : rooms.rooms || [];
    $('room').innerHTML = '';
    list.forEach(room => {
      const option = document.createElement('option'); option.value = room.room_id || room.id; option.textContent = room.name || option.value; $('room').appendChild(option);
    });
    selectedRoom = $('room').value;
    if (!selectedRoom) throw new Error('目前沒有可存取的房間');
    status('已連線 MCP · ' + list.length + ' 個房間');
  }

  $('room').addEventListener('change', () => { selectedRoom = $('room').value; });
  $('refreshRooms').onclick = () => run(loadRooms);
  $('connect').onclick = () => run(async () => { await client.connect(); const tools = await client.listTools(); show(tools); return tools; });
  $('messages').onclick = () => run(async () => { const data = await client.call('smalltalk_list_messages', args({ limit: 80 })); const items = data.messages || data; lastMessageID = items.at?.(-1)?.id || ''; return data; });
  $('articles').onclick = () => run(() => client.call('smalltalk_list_articles', args({ limit: 50, simple: false })));
  $('presence').onclick = () => run(() => client.call('smalltalk_list_presence', args()));
  $('setPresence').onclick = () => run(() => { const statusText = $('presenceStatus').value.trim(); if (!statusText) throw new Error('請輸入 Presence 狀態'); return client.call('smalltalk_set_presence', args({ status: statusText })); });
  $('search').onclick = () => run(async () => { const q = $('query').value.trim(); if (!q) throw new Error('請輸入搜尋文字'); return { rooms: await client.call('smalltalk_search_rooms', { query: q, limit: 50 }), messages: await client.call('smalltalk_search_messages', { query: q, limit: 50 }) }; });
  $('send').onclick = () => run(() => { const text = $('message').value.trim(); if (!text) throw new Error('請輸入訊息'); return client.call('smalltalk_create_article', args({ title: $('title').value.trim() || '瀏覽器發文', text })); });
  $('reply').onclick = () => run(() => { const text = $('replyText').value.trim(); const article = $('articleID').value.trim(); if (!text || !article) throw new Error('請輸入文章 ID 與回覆內容'); return client.call('smalltalk_reply_article', args({ article_id: article, text })); });
  $('poll').onclick = () => run(async () => { const data = await client.call('smalltalk_get_new_messages', args({ after_id: lastMessageID, limit: 80 })); const items = data.messages || data || []; if (items.length) lastMessageID = items[items.length - 1].id || lastMessageID; return data; });
  $('adminAgents').onclick = () => run(() => client.call('smalltalk_admin_list_agents', {}));
  $('logout').onclick = () => { document.cookie = 'smalltalk_auth_token=; Max-Age=0; Path=/'; window.location.replace('/login.html'); };

  run(async () => { await client.connect(); await loadRooms(); return client.listTools(); });
})();
