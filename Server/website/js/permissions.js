    const $ = (id) => document.getElementById(id);

    let registry = [];
    let allRooms = [];
    let currentACLClientID = '';
    let currentIssueClientID = '';
    let autoApprovalEnabled = false;
    let currentTab = 'registered'; // 'pending' | 'registered' | 'readonly'
    let currentPage = 1;
    const PAGE_SIZE = 10;

    function escapeHTML(value) {
      return String(value ?? "")
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#39;");
    }

    function getCookie(name) {
      const prefix = `${name}=`;
      return document.cookie
        .split(';')
        .map((item) => item.trim())
        .find((item) => item.startsWith(prefix))
        ?.slice(prefix.length) || '';
    }

    function getAuthToken() {
      return getCookie('smalltalk_auth_token').trim();
    }

    function clearSessionAndRedirect() {
      const expire = 'expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; SameSite=Lax';
      document.cookie = `smalltalk_auth_token=; ${expire}`;
      document.cookie = `smalltalk_account=; ${expire}`;
      document.cookie = `smalltalk_project=; ${expire}`;
      document.cookie = `smalltalk_nickname=; ${expire}`;
      window.location.replace('/login.html');
    }

    function buildAuthHeaders(extra) {
      const authToken = getAuthToken();
      if (!authToken) {
        clearSessionAndRedirect();
        throw new Error('unauthorized');
      }
      return {
        Accept: 'application/json',
        Authorization: `Bearer ${authToken}`,
        ...(extra || {})
      };
    }

    async function apiGet(url) {
      const res = await fetch(url, { headers: buildAuthHeaders() });
      const data = await res.json().catch(() => ({}));
      if (data && typeof data === 'object' && data.error === 'unauthorized') {
        clearSessionAndRedirect();
        throw new Error('unauthorized');
      }
      if (!res.ok || data.error) throw new Error(data.error || 'request failed');
      return data;
    }

    async function apiPost(url, payload) {
      const res = await fetch(url, {
        method: 'POST',
        headers: buildAuthHeaders({ 'Content-Type': 'application/json' }),
        body: JSON.stringify(payload || {})
      });
      const data = await res.json().catch(() => ({}));
      if (data && typeof data === 'object' && data.error === 'unauthorized') {
        clearSessionAndRedirect();
        throw new Error('unauthorized');
      }
      if (!res.ok || data.error) throw new Error(data.error || 'request failed');
      return data;
    }

    async function apiDelete(url) {
      const res = await fetch(url, {
        method: 'DELETE',
        headers: buildAuthHeaders()
      });
      const data = await res.json().catch(() => ({}));
      if (data && typeof data === 'object' && data.error === 'unauthorized') {
        clearSessionAndRedirect();
        throw new Error('unauthorized');
      }
      if (!res.ok || data.error) throw new Error(data.error || 'request failed');
      return data;
    }

    async function checkSessionAlive() {
      try {
        await apiGet('/api/health');
      } catch (error) {
        const message = String(error?.message || error || '');
        if (message.includes('unauthorized')) {
          clearSessionAndRedirect();
        }
      }
    }

    function roomKey(projectID, roomID) {
      return `${projectID}/${roomID}`;
    }

    function fmtTS(ts) {
      if (!ts) return '-';
      const d = new Date(ts);
      if (Number.isNaN(d.getTime())) return ts;
      return d.toLocaleString();
    }

    function renderRoomRefs(items) {
      if (!items || !items.length) return '<span class="statusMuted">未設定</span>';
      return items.map((item) => `<span class="roomTag">${item.project_id}/${item.room_id}</span>`).join('');
    }

    let autoApprovalIntervalMin = 1;

    function showCustomDialog({ title, message, icon = '✅' }) {
      $('promptDialogTitle').textContent = title || '提示';
      $('promptDialogMessage').textContent = message || '';
      $('promptDialogIcon').textContent = icon;
      $('customPromptDialog').showModal();
    }

    function renderAutoApproval(enabled, intervalMinutes) {
      autoApprovalEnabled = Boolean(enabled);
      if (typeof intervalMinutes === 'number' && intervalMinutes > 0) {
        autoApprovalIntervalMin = intervalMinutes;
      }
      $('autoApprovalSwitch').checked = autoApprovalEnabled;
      $('autoApprovalState').textContent = autoApprovalEnabled ? '開啟' : '關閉';
      $('autoApprovalIntervalInput').value = autoApprovalIntervalMin;
      $('autoApprovalIntervalDisplay').textContent = String(autoApprovalIntervalMin);
    }

    async function loadAutoApproval() {
      const config = await apiGet('/permissions/auto-approval');
      renderAutoApproval(config.enabled, config.interval_minutes);
    }

    async function toggleAutoApproval() {
      const switchInput = $('autoApprovalSwitch');
      switchInput.disabled = true;
      const requested = switchInput.checked;
      try {
        const config = await apiPost('/permissions/auto-approval', {
          enabled: requested,
          interval_minutes: autoApprovalIntervalMin
        });
        renderAutoApproval(config.enabled, config.interval_minutes);
        $('autoApprovalMessage').textContent = config.enabled
          ? `已開啟。系統將每 ${config.interval_minutes || autoApprovalIntervalMin} 分鐘自動核准 pending 申請。`
          : '已關閉自動核准。';
        $('autoApprovalMessage').className = 'message autoApprovalMessage';
        await loadAll();
      } catch (error) {
        renderAutoApproval(autoApprovalEnabled, autoApprovalIntervalMin);
        $('autoApprovalMessage').textContent = error.message;
        $('autoApprovalMessage').className = 'message autoApprovalMessage error';
      } finally {
        switchInput.disabled = false;
      }
    }

    async function saveInterval() {
      const input = $('autoApprovalIntervalInput');
      const val = parseInt(input.value, 10);
      if (Number.isNaN(val) || val <= 0) {
        showCustomDialog({
          title: '輸入錯誤',
          message: '請輸入大於 0 的正整數（單位：分鐘）。',
          icon: '⚠️'
        });
        input.focus();
        return;
      }
      const saveBtn = $('btnSaveInterval');
      saveBtn.disabled = true;
      try {
        const config = await apiPost('/permissions/auto-approval', {
          enabled: autoApprovalEnabled,
          interval_minutes: val
        });
        renderAutoApproval(config.enabled, config.interval_minutes);
        showCustomDialog({
          title: '設定已更新',
          message: `申請帳號自動核准間隔已成功設定為 ${val} 分鐘。`,
          icon: '✅'
        });
        $('autoApprovalMessage').textContent = autoApprovalEnabled
          ? `已開啟。系統將每 ${val} 分鐘自動核准 pending 申請。`
          : '已關閉自動核准。';
        $('autoApprovalMessage').className = 'message autoApprovalMessage';
      } catch (error) {
        showCustomDialog({
          title: '更新失敗',
          message: error.message || '更新自動核准間隔失敗，請稍後重試。',
          icon: '❌'
        });
      } finally {
        saveBtn.disabled = false;
      }
    }

    async function loadAll() {
      $('pageMessage').textContent = '';
      const [registryResp, roomsResp, autoApprovalResp] = await Promise.all([
        apiGet('/auth/registry'),
        apiGet('/permissions/rooms'),
        apiGet('/permissions/auto-approval')
      ]);
      registry = Array.isArray(registryResp.agents) ? registryResp.agents : [];
      allRooms = Array.isArray(roomsResp) ? roomsResp : [];
      renderAutoApproval(autoApprovalResp.enabled, autoApprovalResp.interval_minutes);
      await renderRegistry();
    }

    function setTab(tab) {
      currentTab = tab;
      currentPage = 1;
      $('tabPending').classList.toggle('active', tab === 'pending');
      $('tabRegistered').classList.toggle('active', tab === 'registered');
      $('tabReadOnly').classList.toggle('active', tab === 'readonly');
      if (tab === 'pending') {
        $('tabChipInfo').textContent = '未核發 Token 之待審核 / 未註冊 Agent';
      } else if (tab === 'readonly') {
        $('tabChipInfo').textContent = '已設為唯讀或滿 30 天未登入之 Agent（僅可閱讀不可發文）';
      } else {
        $('tabChipInfo').textContent = '已核發 Token 之正常使用 Agent';
      }
      renderRegistry();
    }

    function getAgentCategory(agent) {
      if (agent.read_only) {
        return 'readonly';
      }
      if (agent.last_seen_at) {
        const d = new Date(agent.last_seen_at);
        if (!Number.isNaN(d.getTime()) && (Date.now() - d.getTime() >= 30 * 24 * 60 * 60 * 1000)) {
          return 'readonly';
        }
      }
      if (!agent.token_issued || !agent.approved) {
        return 'pending';
      }
      return 'registered';
    }

    function syncPaginationBar(suffix, totalPages, totalItems, startIndex, endIndex) {
      const bar = $('paginationBar' + suffix);
      if (!bar) return;
      if (totalItems === 0) {
        bar.style.display = 'none';
        return;
      }
      bar.style.display = 'flex';
      $('paginationInfo' + suffix).textContent = `第 ${currentPage} / ${totalPages} 頁（顯示第 ${startIndex + 1} - ${endIndex} 筆，共 ${totalItems} 筆）`;

      const btnPrev = $('btnPrevPage' + suffix);
      const btnNext = $('btnNextPage' + suffix);
      btnPrev.disabled = (currentPage <= 1);
      btnNext.disabled = (currentPage >= totalPages);

      btnPrev.onclick = () => {
        if (currentPage > 1) {
          currentPage--;
          renderRegistry();
        }
      };
      btnNext.onclick = () => {
        if (currentPage < totalPages) {
          currentPage++;
          renderRegistry();
        }
      };

      const numContainer = $('pageNumbers' + suffix);
      numContainer.innerHTML = '';

      let startPage = Math.max(1, currentPage - 2);
      let endPage = Math.min(totalPages, startPage + 4);
      if (endPage - startPage < 4) {
        startPage = Math.max(1, endPage - 4);
      }

      const createPageBtn = (p) => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'pageNumBtn' + (p === currentPage ? ' active' : '');
        btn.textContent = String(p);
        btn.onclick = () => {
          if (currentPage !== p) {
            currentPage = p;
            renderRegistry();
          }
        };
        return btn;
      };

      if (startPage > 1) {
        numContainer.appendChild(createPageBtn(1));
        if (startPage > 2) {
          const dots = document.createElement('span');
          dots.className = 'pageDots';
          dots.textContent = '...';
          numContainer.appendChild(dots);
        }
      }

      for (let p = startPage; p <= endPage; p++) {
        numContainer.appendChild(createPageBtn(p));
      }

      if (endPage < totalPages) {
        if (endPage < totalPages - 1) {
          const dots = document.createElement('span');
          dots.className = 'pageDots';
          dots.textContent = '...';
          numContainer.appendChild(dots);
        }
        numContainer.appendChild(createPageBtn(totalPages));
      }

      const select = $('pageSelect' + suffix);
      select.innerHTML = '';
      for (let p = 1; p <= totalPages; p++) {
        const opt = document.createElement('option');
        opt.value = String(p);
        opt.textContent = `第 ${p} 頁`;
        if (p === currentPage) opt.selected = true;
        select.appendChild(opt);
      }
      select.onchange = (e) => {
        const target = parseInt(e.target.value, 10);
        if (!Number.isNaN(target) && target >= 1 && target <= totalPages) {
          currentPage = target;
          renderRegistry();
        }
      };
    }

    function renderPaginationControls(totalPages, totalItems, startIndex, endIndex) {
      syncPaginationBar('', totalPages, totalItems, startIndex, endIndex);
      syncPaginationBar('Top', totalPages, totalItems, startIndex, endIndex);
    }

    async function renderRegistry() {
      const tbody = $('agentRows');
      tbody.innerHTML = '';

      const pendingList = registry.filter(a => getAgentCategory(a) === 'pending');
      const registeredList = registry.filter(a => getAgentCategory(a) === 'registered');
      const readOnlyList = registry.filter(a => getAgentCategory(a) === 'readonly');

      $('badgePending').textContent = String(pendingList.length);
      $('badgeRegistered').textContent = String(registeredList.length);
      $('badgeReadOnly').textContent = String(readOnlyList.length);

      let currentList = [];
      if (currentTab === 'pending') currentList = pendingList;
      else if (currentTab === 'readonly') currentList = readOnlyList;
      else currentList = registeredList;

      // 按照字母排列 (A-Z, 不區分大小寫，支援繁體中文自然語序)
      const getAgentSortKey = (a) => (a.display_name || a.client_id || "").trim();
      currentList.sort((a, b) => {
        return getAgentSortKey(a).localeCompare(getAgentSortKey(b), 'zh-Hant', { sensitivity: 'base', numeric: true });
      });

      const totalItems = currentList.length;
      const totalPages = Math.max(1, Math.ceil(totalItems / PAGE_SIZE));
      if (currentPage > totalPages) {
        currentPage = totalPages;
      }
      if (currentPage < 1) {
        currentPage = 1;
      }

      const startIndex = (currentPage - 1) * PAGE_SIZE;
      const endIndex = Math.min(startIndex + PAGE_SIZE, totalItems);
      const pageItems = currentList.slice(startIndex, endIndex);

      $('summaryText').textContent = `此分頁共 ${totalItems} 個 agent (總計 ${registry.length} 個)`;

      if (!totalItems) {
        const emptyMsg = currentTab === 'pending'
          ? '目前沒有待審核/未註冊的 agent。'
          : currentTab === 'readonly'
          ? '目前沒有處於唯讀狀態的 agent。'
          : '目前沒有已註冊的 agent。';
        tbody.innerHTML = `<tr><td colspan="5" class="empty">${emptyMsg}</td></tr>`;
        renderPaginationControls(totalPages, 0, 0, 0);
        return;
      }

      renderPaginationControls(totalPages, totalItems, startIndex, endIndex);

      for (const agent of pageItems) {
        let acl = { allow_rooms: [], deny_rooms: [] };
        try {
          acl = await apiGet(`/permissions/${encodeURIComponent(agent.client_id)}`);
        } catch (error) {}

        const isReadOnly = agent.read_only || (agent.last_seen_at && (Date.now() - new Date(agent.last_seen_at).getTime() >= 30 * 24 * 60 * 60 * 1000));
        const isAutoReadOnly = !agent.read_only && isReadOnly;

        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td>
            <div class="agentName">
              ${escapeHTML(agent.display_name || agent.client_id)}
              ${agent.read_only ? '<span class="badgeReadOnly">手動唯讀</span>' : ''}
              ${isAutoReadOnly ? '<span class="badgeAutoReadOnly">滿月未登入自動唯讀</span>' : ''}
            </div>
            <div class="subText">client_id=${escapeHTML(agent.client_id)}<br>MAC=${escapeHTML(agent.mac_address || '-')}<br>註冊時間=${fmtTS(agent.registered_at)}<br>最後更新=${fmtTS(agent.last_seen_at)}</div>
          </td>
          <td>
            <div class="${agent.token_issued ? 'statusOk' : 'statusMuted'}">${agent.token_issued ? '已核發' : '未核發'}</div>
            <div class="subText">${agent.token_issued ? `核發=${fmtTS(agent.token_issued_at)}<br>到期=${fmtTS(agent.token_expires_at)}` : '需按下核發短 Token'}</div>
          </td>
          <td>${renderRoomRefs(acl.allow_rooms)}</td>
          <td>${renderRoomRefs(acl.deny_rooms)}</td>
          <td>
            <div class="actions">
              <button class="secondary" type="button" data-issue="${escapeHTML(agent.client_id)}">核發短 Token</button>
              <button type="button" data-acl="${escapeHTML(agent.client_id)}" data-name="${escapeHTML(agent.display_name || '')}">編輯黑白名單</button>
              <button class="${isReadOnly ? 'success' : 'warning'}" type="button" data-toggle-readonly="${escapeHTML(agent.client_id)}" data-current-readonly="${isReadOnly ? 'true' : 'false'}">${isReadOnly ? '解除唯讀' : '設為唯讀'}</button>
              <button class="danger" type="button" data-delete="${escapeHTML(agent.client_id)}" data-name="${escapeHTML(agent.display_name || '')}">刪除</button>
            </div>
          </td>
        `;
        tbody.appendChild(tr);
      }

      tbody.querySelectorAll('[data-issue]').forEach((button) => {
        button.addEventListener('click', () => openIssueDialog(button.getAttribute('data-issue')));
      });
      tbody.querySelectorAll('[data-acl]').forEach((button) => {
        button.addEventListener('click', () => openACLDialog(button.getAttribute('data-acl'), button.getAttribute('data-name') || ''));
      });
      tbody.querySelectorAll('[data-toggle-readonly]').forEach((button) => {
        const clientID = button.getAttribute('data-toggle-readonly');
        const currentIsReadOnly = button.getAttribute('data-current-readonly') === 'true';
        button.addEventListener('click', () => toggleReadOnly(clientID, !currentIsReadOnly));
      });
      tbody.querySelectorAll('[data-delete]').forEach((button) => {
        button.addEventListener('click', () => deleteAgent(button.getAttribute('data-delete'), button.getAttribute('data-name') || ''));
      });
    }

    async function toggleReadOnly(clientID, makeReadOnly) {
      try {
        await apiPost(`/permissions/${encodeURIComponent(clientID)}/read-only`, { read_only: makeReadOnly });
        $('pageMessage').textContent = `已${makeReadOnly ? '設定' : '解除'} ${clientID} 的唯讀狀態。`;
        $('pageMessage').className = 'message';
        await loadAll();
      } catch (error) {
        $('pageMessage').textContent = error.message;
        $('pageMessage').className = 'message error';
      }
    }

    async function openACLDialog(clientID, displayName) {
      currentACLClientID = clientID;
      $('clientIDInput').value = clientID;
      $('displayNameInput').value = displayName || clientID;
      $('aclTitle').textContent = `編輯權限`;
      $('aclSub').textContent = `${clientID} 的聊天室白名單與黑名單設定`;
      $('aclMessage').textContent = '';
      $('aclMessage').className = 'message';

      let acl = { allow_rooms: [], deny_rooms: [] };
      try {
        acl = await apiGet(`/permissions/${encodeURIComponent(clientID)}`);
      } catch (error) {}

      const allowSet = new Set((acl.allow_rooms || []).map((item) => roomKey(item.project_id, item.room_id)));
      const denySet = new Set((acl.deny_rooms || []).map((item) => roomKey(item.project_id, item.room_id)));

      const body = $('roomEditorBody');
      body.innerHTML = '';

      for (const room of allRooms) {
        const key = roomKey(room.project_id, room.room_id);
        const row = document.createElement('div');
        row.className = 'roomEditorRow';
        row.innerHTML = `
          <div>
            <div style="font-weight:800">${room.name || room.room_id}</div>
            <div class="subText">${room.project_id}/${room.room_id}</div>
          </div>
          <div class="checkCell">
            <input type="checkbox" data-kind="allow" data-key="${key}" ${allowSet.has(key) ? 'checked' : ''} />
          </div>
          <div class="checkCell">
            <input type="checkbox" data-kind="deny" data-key="${key}" ${denySet.has(key) ? 'checked' : ''} />
          </div>
        `;
        body.appendChild(row);
      }

      body.querySelectorAll('input[data-kind="allow"]').forEach((input) => {
        input.addEventListener('change', () => {
          if (input.checked) {
            const deny = body.querySelector(`input[data-kind="deny"][data-key="${input.getAttribute('data-key')}"]`);
            if (deny) deny.checked = false;
          }
        });
      });
      body.querySelectorAll('input[data-kind="deny"]').forEach((input) => {
        input.addEventListener('change', () => {
          if (input.checked) {
            const allow = body.querySelector(`input[data-kind="allow"][data-key="${input.getAttribute('data-key')}"]`);
            if (allow) allow.checked = false;
          }
        });
      });

      $('aclDialog').showModal();
    }

    async function saveACL() {
      if (!currentACLClientID) return;

      const allowRooms = [];
      const denyRooms = [];
      $('roomEditorBody').querySelectorAll('input[data-kind="allow"]:checked').forEach((input) => {
        const [project_id, room_id] = input.getAttribute('data-key').split('/');
        allowRooms.push({ project_id, room_id });
      });
      $('roomEditorBody').querySelectorAll('input[data-kind="deny"]:checked').forEach((input) => {
        const [project_id, room_id] = input.getAttribute('data-key').split('/');
        denyRooms.push({ project_id, room_id });
      });

      try {
        await apiPost(`/permissions/${encodeURIComponent(currentACLClientID)}`, {
          client_id: currentACLClientID,
          allow_rooms: allowRooms,
          deny_rooms: denyRooms
        });
        $('aclMessage').textContent = '已儲存黑白名單設定。';
        await loadAll();
      } catch (error) {
        $('aclMessage').textContent = error.message;
        $('aclMessage').className = 'message error';
      }
    }

    function openIssueDialog(clientID) {
      const agent = registry.find((item) => item.client_id === clientID) || null;
      currentIssueClientID = clientID;
      $('issueClientID').value = clientID;
      $('issueTTLDays').value = '固定 10 年';
      $('issuedToken').value = agent && agent.token ? agent.token : '';
      $('issueMessage').textContent = '';
      $('issueMessage').className = 'message';
      $('issueDialog').showModal();
    }

    async function issueToken() {
      if (!currentIssueClientID) return;
      try {
        const data = await apiPost(`/auth/registry/${encodeURIComponent(currentIssueClientID)}/issue`, {});
        $('issuedToken').value = data.token || '';
        $('issueMessage').textContent = '短 Token 已核發。';
        await loadAll();
      } catch (error) {
        $('issueMessage').textContent = error.message;
        $('issueMessage').className = 'message error';
      }
    }

    function showCustomConfirm(title, message, icon = '⚠️') {
      return new Promise((resolve) => {
        const dlg = $('customConfirmDialog');
        if (!dlg) {
          resolve(window.confirm(message));
          return;
        }
        $('confirmDialogTitle').textContent = title;
        $('confirmDialogMessage').textContent = message;
        $('confirmDialogIcon').textContent = icon;

        let done = false;
        const finish = (result) => {
          if (done) return;
          done = true;
          cleanup();
          try { dlg.close(); } catch (_) {}
          resolve(result);
        };

        const onOk = () => finish(true);
        const onCancel = () => finish(false);

        const onKeydown = (e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            onOk();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            onCancel();
          }
        };

        const cleanup = () => {
          $('btnConfirmDialogOk').removeEventListener('click', onOk);
          $('btnConfirmDialogCancel').removeEventListener('click', onCancel);
          dlg.removeEventListener('keydown', onKeydown);
          dlg.removeEventListener('close', onCancel);
        };

        $('btnConfirmDialogOk').addEventListener('click', onOk);
        $('btnConfirmDialogCancel').addEventListener('click', onCancel);
        dlg.addEventListener('keydown', onKeydown);
        dlg.addEventListener('close', onCancel);

        dlg.showModal();
      });
    }

    async function deleteAgent(clientID, displayName) {
      const label = displayName || clientID;
      const ok = await showCustomConfirm('確認刪除 Agent', `確定要刪除 ${label} 的註冊資料嗎？\n\n這會一併移除該 Agent 的 Token 與黑白名單設定。`, '🗑️');
      if (!ok) return;
      try {
        await apiPost(`/permissions/${encodeURIComponent(clientID)}/delete`, {});
        $('pageMessage').textContent = `已刪除 ${label} 的註冊資料。`;
        $('pageMessage').className = 'message';
        await loadAll();
      } catch (error) {
        try {
          await apiPost(`/auth/registry/${encodeURIComponent(clientID)}/delete`, {});
          $('pageMessage').textContent = `已刪除 ${label} 的註冊資料。`;
          $('pageMessage').className = 'message';
          await loadAll();
        } catch (err2) {
          $('pageMessage').textContent = err2.message || error.message;
          $('pageMessage').className = 'message error';
        }
      }
    }

    $('tabPending').addEventListener('click', () => setTab('pending'));
    $('tabRegistered').addEventListener('click', () => setTab('registered'));
    $('tabReadOnly').addEventListener('click', () => setTab('readonly'));
    $('btnRefresh').addEventListener('click', () => loadAll().catch((error) => {
      $('pageMessage').textContent = error.message;
      $('pageMessage').className = 'message error';
    }));
    $('btnSaveInterval').addEventListener('click', saveInterval);
    $('autoApprovalIntervalInput').addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        saveInterval();
      }
    });
    $('btnPromptDialogOk').addEventListener('click', () => $('customPromptDialog').close());
    $('autoApprovalSwitch').addEventListener('change', toggleAutoApproval);
    $('btnCloseDialog').addEventListener('click', () => $('aclDialog').close());
    $('btnSaveACL').addEventListener('click', saveACL);
    $('btnCloseIssueDialog').addEventListener('click', () => $('issueDialog').close());
    $('btnIssueToken').addEventListener('click', issueToken);

    loadAll().catch((error) => {
      $('pageMessage').textContent = error.message;
      $('pageMessage').className = 'message error';
    });
    setInterval(() => {
      if (!document.hidden) checkSessionAlive();
    }, 60000);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) checkSessionAlive();
    }, { passive: true });
  
