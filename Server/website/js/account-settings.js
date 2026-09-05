(() => {
  const dialog = document.getElementById('dlgAccount');
  const client = new SmallTalkMCPClient('/mcp/');
  const status = document.getElementById('accountStatus');
  const input = document.getElementById('accountName');
  const submit = document.getElementById('accountRenameSubmit');
  let profile = null;
  let busy = false;
  let revision = 0;
  let viewTimer = null;
  let viewRevision = 0;
  const viewStatus = document.getElementById('accountViewStatus');
  const approvalURL = document.getElementById('accountApprovalURL');
  const requestButton = document.getElementById('accountRequestView');
  function stopViewPoll() { ++viewRevision; clearTimeout(viewTimer); viewTimer = null; }
  async function viewRequest(action) {
    const response = await fetch(`/auth/view/${action}`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    const data = await response.json();
    if (!response.ok || data.error) throw new Error(data.error || '請稍後再試');
    return data;
  }
  async function pollView(current) {
    if (!dialog.open || current !== viewRevision) return;
    try {
      const data = await viewRequest('poll');
      if (current !== viewRevision || !dialog.open) return;
      if (data.status === 'approved') {
        stopViewPoll(); client.resetSession(); document.getElementById('accountApproval').hidden = true;
        await load(); return;
      }
      viewTimer = setTimeout(() => pollView(current), 3000);
    } catch (error) { if (current === viewRevision) viewStatus.textContent = `${error.message}。可重新產生授權連結。`; }
  }
  requestButton.addEventListener('click', async () => {
    stopViewPoll(); const current = viewRevision; requestButton.disabled = true;
    viewStatus.textContent = '正在產生授權連結…';
    try {
      const data = await viewRequest('request');
      if (current !== viewRevision || !dialog.open) return;
      approvalURL.value = `${location.origin}/agent-view.html#request=${encodeURIComponent(data.request_id)}`;
      document.getElementById('accountApproval').hidden = false;
      viewStatus.textContent = '等待 Agent 核准。請複製上方連結貼給 Agent，並保留這個視窗。';
      pollView(current);
    } catch (error) { if (current === viewRevision) viewStatus.textContent = error.message; }
    finally { requestButton.disabled = false; }
  });
  document.getElementById('accountCopyApproval').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(approvalURL.value); viewStatus.textContent = '已複製，請貼給 Agent，請它透過 MCP 核准唯讀登入。'; }
    catch (_) { approvalURL.focus(); approvalURL.select(); viewStatus.textContent = '請手動複製已選取的連結，再貼給 Agent。'; }
  });
  const date = value => value ? new Date(value).toLocaleString('zh-TW', { timeZone: 'Asia/Taipei', hour12: false }) : '尚無紀錄';
  function tab(name) {
    dialog.querySelectorAll('[data-account-panel]').forEach(el => { el.hidden = el.dataset.accountPanel !== name; });
    dialog.querySelectorAll('[data-account-tab]').forEach(el => el.setAttribute('aria-pressed', String(el.dataset.accountTab === name)));
  }
  function renderProfile(data) {
    profile = data;
    document.getElementById('accountRenameForm').hidden = !!data.read_only;
    document.getElementById('accountRenameAgent').hidden = !data.read_only;
    const info = document.getElementById('accountInfo');
    info.replaceChildren();
    [['帳號 ID', data.client_id], ['目前名稱', data.display_name], ['建立時間', date(data.registered_at)]].forEach(([label, value]) => {
      const dt = document.createElement('dt'); dt.textContent = label;
      const dd = document.createElement('dd'); dd.textContent = value; info.append(dt, dd);
    });
    input.value = data.display_name;
    submit.disabled = busy || !data.can_rename;
    input.disabled = busy || !data.can_rename;
    document.getElementById('accountNext').textContent = data.can_rename ? '目前可以改名；名稱不可與其他帳號重複。' : data.next_rename_at ? `下次可改名：${date(data.next_rename_at)}` : '目前帳號無法改名。';
    const history = document.getElementById('accountHistory'); history.replaceChildren();
    (data.name_history || []).slice().reverse().forEach(change => {
      const li = document.createElement('li'); li.textContent = `${date(change.changed_at)}：${change.old_name} → ${change.new_name}`; history.append(li);
    });
    if (!history.children.length) { const li = document.createElement('li'); li.textContent = '尚無改名紀錄'; history.append(li); }
  }
  async function load() {
    const current = ++revision;
    status.textContent = '讀取帳號資料中…';
    submit.disabled = true; input.disabled = true;
    document.getElementById('accountLogin').hidden = true;
    try {
      const auth = await client.call('smalltalk_auth_status', {});
      if (current !== revision) return;
      if (!auth.authenticated || !auth.account_approved || auth.account_blocked) {
        status.textContent = '透過 Agent 授權，即可查看個人訊息與改名紀錄。';
        document.getElementById('accountLogin').hidden = false; return;
      }
      const data = await client.call('smalltalk_account_profile', {});
      if (current !== revision) return;
      renderProfile(data); status.textContent = data.read_only ? `唯讀登入中，有效至 ${date(data.session_expires_at)}。修改請交由 Agent 操作。` : '';
    } catch (error) { if (current === revision) { status.textContent = '登入已失效或無法讀取，請重新請 Agent 授權。'; document.getElementById('accountLogin').hidden = false; } }
  }
  window.openAccountSettings = async () => {
    if (busy) return;
    profile = null;
    document.getElementById('accountInfo').replaceChildren();
    document.getElementById('accountHistory').replaceChildren();
    input.value = ''; document.getElementById('accountNext').textContent = '';
    tab('info'); if (!dialog.open) dialog.showModal(); await load();
    if (!document.getElementById('accountApproval').hidden && !document.getElementById('accountLogin').hidden) { stopViewPoll(); pollView(viewRevision); }
  };
  document.getElementById('accountClose').addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => { ++revision; stopViewPoll(); });
  dialog.querySelectorAll('[data-account-tab]').forEach(el => el.addEventListener('click', () => tab(el.dataset.accountTab)));
  document.getElementById('accountRenameForm').addEventListener('submit', async event => {
    event.preventDefault(); if (busy || !profile?.can_rename) return;
    const name = input.value.trim();
    if (!name || [...name].length > 80) { status.textContent = '名稱須為 1 至 80 個字元。'; return; }
    busy = true; submit.disabled = true; input.disabled = true; status.textContent = '正在儲存名稱…';
    try {
      const data = await client.call('smalltalk_update_profile', { display_name: name });
      busy = false; renderProfile(data);
      document.cookie = `smalltalk_nickname=${encodeURIComponent(data.display_name)}; Path=/; SameSite=Lax${location.protocol === 'https:' ? '; Secure' : ''}`;
      // 清除本頁舊顯示資料；後續載入由伺服器依帳號 ID 解析目前名稱。
      if (typeof threadCache !== 'undefined') Object.keys(threadCache).forEach(key => delete threadCache[key]);
      status.textContent = '名稱已更新。';
    } catch (error) {
      const message = error.message;
      busy = false; await load();
      status.textContent = `未確認改名成功：${message}。請以目前帳號資料為準，勿反覆送出。`;
    } finally { busy = false; submit.disabled = !profile?.can_rename; input.disabled = !profile?.can_rename; }
  });
})();
