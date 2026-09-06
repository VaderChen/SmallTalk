/* LIVE 公開閱讀；沿用 BBS 層級、方向鍵及滑動導覽。 */
const liveChat = {rooms: [], index: 0, room: null, messages: [], seq: 0, cursor: '', more: false, status: '', error: '', busy: false, generation: 0, timer: null, controller: null};
function isPublicChatLevel() { return state.level === 'live_rooms' || state.level === 'live_chat'; }
function stopPublicChat() {
  clearTimeout(liveChat.timer);
  liveChat.controller?.abort();
  liveChat.generation++;
  liveChat.busy = false;
}
function openPublicChatrooms() {
  stopPublicChat();
  Object.assign(liveChat, {rooms: [], index: 0, room: null, messages: [], cursor: '', seq: 0, more: false, status: '讀取中…', error: ''});
  state.level = 'live_rooms'; render(); fetchPublicChat(false);
}
function publicChatEnter() {
  if (state.level !== 'live_rooms') return;
  const room = liveChat.rooms[liveChat.index]; if (!room) return;
  stopPublicChat();
  Object.assign(liveChat, {room, messages: [], seq: 0, more: false, status: '讀取中…', error: ''});
  state.level = 'live_chat'; render(); fetchPublicChat(false);
}
function publicChatBack() {
  if (state.level === 'live_chat') {openPublicChatrooms(); return;}
  stopPublicChat(); state.level = 'menu'; render();
}
function publicChatMove(delta) {
  if (state.level === 'live_chat') {document.getElementById('publicChatView').scrollBy(0, delta * 48); return;}
  liveChat.index = Math.max(0, Math.min(liveChat.rooms.length - 1, liveChat.index + delta));
  render(); document.querySelector('#publicChatContent .activeRow')?.scrollIntoView({block: 'nearest'});
}
function publicChatKey(event) {
  const key = event.key;
  if (event.target.closest?.('button') && (key === 'Enter' || key === ' ')) return;
  if (!['ArrowUp','ArrowDown','PageUp','PageDown','ArrowLeft','ArrowRight','Enter','Escape','r','R','n','N','Home','End'].includes(key)) return;
  event.preventDefault();
  if (key === 'ArrowLeft' || key === 'Escape') return publicChatBack();
  if (key === 'r' || key === 'R') return refreshPublicChat();
  if (key === 'n' || key === 'N') return loadPublicChatMore();
  if (key === 'Enter' || key === 'ArrowRight') return publicChatEnter();
  if (key === 'Home' || key === 'End') return publicChatMove(key === 'Home' ? -100000 : 100000);
  publicChatMove(({ArrowUp:-1,ArrowDown:1,PageUp:-10,PageDown:10})[key]);
}
function refreshPublicChat() {
  if (state.level === 'live_rooms') return openPublicChatrooms();
  stopPublicChat(); liveChat.status = '更新中…'; fetchPublicChat(false);
}
function loadPublicChatMore() {if (liveChat.more) fetchPublicChat(true);}
async function fetchPublicChat(append) {
  if (!isPublicChatLevel() || liveChat.busy) return;
  clearTimeout(liveChat.timer);
  liveChat.busy = true;
  const version = liveChat.generation, inRoom = state.level === 'live_chat';
  const controller = new AbortController(); liveChat.controller = controller;
  const timeout = setTimeout(() => controller.abort(), 15000);
  try {
    const path = inRoom ? `/${encodeURIComponent(liveChat.room.room_id)}?after_seq=${liveChat.seq}` : `?after_id=${encodeURIComponent(append ? liveChat.cursor : '')}`;
    const response = await fetch(`/api/chatrooms${path}`, {cache: 'no-store', signal: controller.signal});
    let data = await response.json();
    // 自動更新已展開的列表頁面，保留分頁進度與選取位置。
    if (!inRoom && !append && response.ok && Array.isArray(data.chatrooms)) {
      const wanted = liveChat.rooms.length;
      const rows = [...data.chatrooms];
      while (rows.length < wanted && data.has_more) {
        const nextResponse = await fetch(`/api/chatrooms?after_id=${encodeURIComponent(data.next_after_id)}`, {cache:'no-store',signal:controller.signal});
        if (!nextResponse.ok) throw new Error('聊天室列表更新失敗');
        data = await nextResponse.json(); rows.push(...data.chatrooms);
      }
      data.chatrooms = rows;
    }
    if (version !== liveChat.generation || !isPublicChatLevel()) return;
    if (!response.ok || data.error) {
      // 關閉或移除時清除已顯示內容，不留下可繼續翻閱的前端快照。
      if (response.status === 404 && inRoom) {
        openPublicChatrooms(); liveChat.status = '聊天室已關閉或不存在，停止公開閱覽。'; render(); return;
      }
      throw new Error(data.error || '讀取失敗');
    }
    if (inRoom) {
      liveChat.room = data;
      liveChat.messages.push(...data.messages);
      liveChat.seq = data.next_after_seq;
    } else {
      const selected = liveChat.rooms[liveChat.index]?.room_id;
      liveChat.rooms = append ? liveChat.rooms.concat(data.chatrooms) : data.chatrooms;
      liveChat.index = Math.max(0, liveChat.rooms.findIndex(r => r.room_id === selected));
      liveChat.cursor = data.next_after_id;
    }
    liveChat.error = '';
    liveChat.more = data.has_more;
    liveChat.status = inRoom ? (liveChat.messages.length ? 'LIVE・唯讀，每 5 秒更新' : 'LIVE・尚無發言，每 5 秒更新') : (liveChat.rooms.length ? '僅列出 LIVE 聊天室，任何人都能觀看。' : '目前沒有 LIVE 聊天室。');
    render();
  } catch (error) {
    if (version === liveChat.generation && isPublicChatLevel()) {
      // 連線失敗無法確認仍為 LIVE，因此暫停呈現舊對話。
      if (inRoom) {liveChat.messages = []; liveChat.seq = 0;}
      liveChat.status = `暫時無法讀取，將重試。${error.message}`; liveChat.error = liveChat.status; render();
    }
  } finally {
    clearTimeout(timeout);
    if (version === liveChat.generation && isPublicChatLevel()) {
      liveChat.busy = false;
      // 即使仍有歷史分頁，也定期檢查聊天室是否已關閉。
      liveChat.timer = setTimeout(() => fetchPublicChat(false), 5000);
    }
  }
}
// 以固定帳號識別配色；同名或改名不會混淆，同一頁中避免色相重複。
const publicSpeakerColors = new Map();
const publicSpeakerHues = new Set();
function publicSpeakerColor(message) {
  const identity = message.author_id || message.author || '?';
  if (!publicSpeakerColors.has(identity)) {
    let hash = 2166136261;
    for (const char of identity) hash = Math.imul(hash ^ char.codePointAt(0), 16777619) >>> 0;
    let hue = hash % 360;
    for (let attempt = 0; attempt < 360 && publicSpeakerHues.has(hue); attempt++) hue = (hue + 137) % 360;
    publicSpeakerHues.add(hue);
    publicSpeakerColors.set(identity, `hsl(${hue} 72% 74%)`);
  }
  return publicSpeakerColors.get(identity);
}
function publicChatNode(tag, text) {const n = document.createElement(tag); n.textContent = text; return n;}
function renderPublicChat() {
  const view = document.getElementById('publicChatView'), content = document.getElementById('publicChatContent');
  const atBottom = view.scrollHeight - view.scrollTop - view.clientHeight < 60;
  const scroll = view.scrollTop;
  const fragment = document.createDocumentFragment();
  if (state.level === 'live_rooms') {
    liveChat.rooms.forEach((room,index) => {
      const row = document.createElement('button'); row.type = 'button'; row.className = 'liveRoomRow' + (index === liveChat.index ? ' activeRow' : '');
      row.append(publicChatNode('span', String(index + 1).padStart(4,'0')),publicChatNode('span',room.name),publicChatNode('span',room.owner_name),publicChatNode('span','LIVE'),publicChatNode('span',String(room.active_participant_count ?? 0)),publicChatNode('span',room.join_mode === 'public' ? '公開' : '僅限好友'));
      row.onclick = () => {liveChat.index = index; publicChatEnter();}; fragment.append(row);
    });
  } else {
    for (const message of liveChat.messages) {
      const row = document.createElement('article'); row.className = 'liveMessage';
      row.style.setProperty('--speaker-color', publicSpeakerColor(message));
      const avatar = publicChatNode('span', Array.from(message.author || '?')[0]);
      avatar.className = 'liveAvatar'; avatar.setAttribute('aria-hidden', 'true');
      const body = document.createElement('div'); body.className = 'liveMessageBody';
      const line = document.createElement('div'); line.className = 'liveMessageLine';
      const timestamp = publicChatNode('time', new Date(message.created_at).toLocaleString('zh-TW'));
      timestamp.dateTime = message.created_at;
      line.append(publicChatNode('p', message.text), timestamp);
      body.append(publicChatNode('strong', message.author), line);
      row.append(avatar, body); fragment.append(row);
    }
  }
  if (!fragment.childNodes.length) fragment.append(publicChatNode('p',liveChat.status));
  content.replaceChildren(fragment);
  view.scrollTop = state.level === 'live_chat' && atBottom ? view.scrollHeight : scroll;
}
function renderPublicChatChrome() {
  const inRoom = state.level === 'live_chat';
  breadcrumb.textContent = '主選單｜聊天專區' + (inRoom ? `｜${liveChat.room.name}` : '｜LIVE 聊天室');
  const toolbar = `<button type="button" data-action="prev"><span class="hotkey">←/Esc)</span>${inRoom ? '返回聊天室列表' : '返回選單'}</button> <button type="button" data-action="chat-refresh"><span class="hotkey">r)</span>重新整理</button>` + (!inRoom ? ' <button type="button" data-action="next"><span class="hotkey">→/Enter)</span>進入聊天室</button>' : ' <span>↑↓/PgUp/PgDn 捲動・唯讀</span>') + (liveChat.more ? ' <button type="button" data-action="chat-more"><span class="hotkey">n)</span>載入更多</button>' : '');
  if (subBar.innerHTML !== toolbar) subBar.innerHTML = toolbar;
  tableHead.className = inRoom ? 'tableHead' : 'tableHead liveRoomHead';
  if (inRoom) tableHead.textContent = `LIVE・${liveChat.room.join_mode === 'public' ? '公開加入' : '好友邀請'}・在線參與者 ${liveChat.room.active_participant_count ?? 0} 人`;
  else tableHead.innerHTML = '<span>編號</span><span>聊天室名稱</span><span>發起者</span><span>狀態</span><span>在線</span><span>加入方式</span>';
  noticeBar.textContent = liveChat.error || '';
  footerText.textContent = liveChat.status;
}

// 聊天專區使用原生垂直捲動；橫向滑動只在內容區處理，避免工具列誤觸。
(() => {
  const view = document.getElementById('publicChatView');
  let start = null, suppressClickUntil = 0;
  view.addEventListener('touchstart', event => {
    start = event.touches.length === 1 ? {x:event.touches[0].clientX,y:event.touches[0].clientY,time:performance.now()} : null;
  }, {passive:true});
  view.addEventListener('touchmove', event => {
    if (event.touches.length !== 1) start = null;
  }, {passive:true});
  view.addEventListener('touchcancel', () => {start = null;}, {passive:true});
  view.addEventListener('touchend', event => {
    if (!start || !isPublicChatLevel()) return;
    const origin = start; start = null;
    const touch = event.changedTouches[0]; if (!touch) return;
    const dx = touch.clientX-origin.x, dy = touch.clientY-origin.y;
    if (Math.abs(dx)>12 || Math.abs(dy)>12) suppressClickUntil = performance.now()+350;
    if (Math.abs(dx)<80 || Math.abs(dx)<Math.abs(dy)*1.8 || performance.now()-origin.time>600) return;
    if (window.getSelection()?.toString()) return;
    if (dx>0) publicChatBack(); else publicChatEnter();
  }, {passive:true});
  view.addEventListener('click', event => {
    if (performance.now()<suppressClickUntil) {event.preventDefault();event.stopImmediatePropagation();}
  }, true);
})();
