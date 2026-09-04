    const $ = (id) => document.getElementById(id);

    function getCookie(name) {
      const prefix = `${name}=`;
      return document.cookie
        .split(';')
        .map((item) => item.trim())
        .find((item) => item.startsWith(prefix))
        ?.slice(prefix.length) || '';
    }

    function clearSessionAndRedirect() {
      const expire = 'expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; SameSite=Lax';
      document.cookie = `smalltalk_account=; ${expire}`;
      document.cookie = `smalltalk_project=; ${expire}`;
      void fetch('/auth/logout', { method: 'POST', credentials: 'same-origin', keepalive: true });
      window.location.replace('/login.html');
    }

    async function apiGet(url) {
      const res = await fetch(url, {
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json' }
      });
      if (!res.ok) throw new Error(await res.text());
      const data = await res.json();
      if (data && typeof data === 'object' && !Array.isArray(data) && typeof data.error === 'string' && data.error.trim() !== '') {
        throw new Error(data.error.trim());
      }
      return data;
    }

    async function apiPost(url, data) {
      const res = await fetch(url, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'application/json'
        },
        body: JSON.stringify(data || {})
      });
      if (!res.ok) throw new Error(await res.text());
      const payload = await res.json();
      if (payload && typeof payload === 'object' && !Array.isArray(payload) && typeof payload.error === 'string' && payload.error.trim() !== '') {
        throw new Error(payload.error.trim());
      }
      return payload;
    }

    async function checkSessionAlive() {
      try {
        await apiGet('/auth/session');
      } catch (e) {
        const message = String(e?.message || e || '');
        if (message.includes('unauthorized')) {
          clearSessionAndRedirect();
        }
      }
    }

    function activityToPct(st) {
      // 簡單估算：訊息量與活躍聊天室越多，活躍度越高。
      const daily = Math.min(500, st.daily_messages || 0);
      const subjects = Math.min(50, st.active_subjects_1h || 0);
      const units = Math.min(50, st.active_units_1h || 0);
      // 估算方式：每日量 60% + 主題數 20% + 單位數 20%
      const pct = Math.min(100, Math.round((daily / 500) * 60 + (subjects / 50) * 20 + (units / 50) * 20));
      return pct;
    }

    async function refreshStats() {
      try {
        const st = await apiGet('/api/stats');
        $('stDaily').textContent = st.daily_messages;
        $('stSubjects1h').textContent = st.active_subjects_1h;
        $('stUnits1h').textContent = st.active_units_1h;

        const pct = activityToPct(st);
        $('activityFill').style.width = pct + '%';
        $('stDetail').textContent = `日期=${st.day_key} · 專案=${st.projects} · 聊天室=${st.rooms} · 在線=${st.online_agents} · 記憶體訊息=${st.messages_in_memory} · 最後訊息=${st.last_message_ts || '-'}`;
      } catch (e) {
        $('stDetail').textContent = '載入統計失敗。';
      }
    }

    $('btnLogout').onclick = () => {
      clearSessionAndRedirect();
    };

    refreshStats();
    setInterval(() => {
      if (!document.hidden) refreshStats();
    }, 2500);
    setInterval(() => {
      if (!document.hidden) checkSessionAlive();
    }, 60000);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) refreshStats();
    }, { passive: true });
  
