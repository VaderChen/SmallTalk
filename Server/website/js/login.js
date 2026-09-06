    const $ = (id) => document.getElementById(id);

    function getCookie(name) {
      const prefix = name + '=';
      const parts = document.cookie ? document.cookie.split(';') : [];
      for (const raw of parts) {
        const item = raw.trim();
        if (item.startsWith(prefix)) return decodeURIComponent(item.slice(prefix.length));
      }
      return '';
    }

    async function loadProjects() {
      const projectSelect = $('project');
      const preferred = getCookie('smalltalk_project');

      try {
        const res = await fetch('/auth/projects', { headers: { 'Accept': 'application/json' } });
        const data = await res.json();
        const projects = Array.isArray(data.projects) ? data.projects : [];
        projectSelect.innerHTML = '';

        for (const project of projects) {
          const option = document.createElement('option');
          option.value = project.id || '';
          option.textContent = project.name && project.name !== project.id
            ? `${project.name} (${project.id})`
            : (project.id || '');
          projectSelect.appendChild(option);
        }

        if (!projects.length) {
          const option = document.createElement('option');
          option.value = '';
          option.textContent = '沒有可用專案';
          projectSelect.appendChild(option);
        }

        if (preferred) {
          projectSelect.value = preferred;
        }
      } catch (error) {
        projectSelect.innerHTML = '<option value="">default</option>';
      }
    }

    async function submitLogin(event) {
      event.preventDefault();
      $('error').textContent = '';
      $('submitBtn').disabled = true;

      const payload = {
        account: $('account').value.trim(),
        password: $('password').value,
        project: $('project').value
      };

      try {
        const res = await fetch('/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
          body: JSON.stringify(payload)
        });
        const data = await res.json();
        if (!res.ok || data.error) {
          throw new Error(res.status === 403 ? '登入請求遭拒絕，請重新整理後再試或聯絡管理員。' : (data.error || '登入服務暫時無法使用。'));
        }
        window.location.replace('/main.html');
      } catch (error) {
        $('error').textContent = error.message === 'login failed' ? '請輸入正確帳號密碼' : (error.message || '無法連線至登入服務，請稍後再試。');
        $('submitBtn').disabled = false;
      }
    }

    (async () => {
      const requireAdminLogin = new URLSearchParams(window.location.search).get('reason') === 'admin_required';
      if (requireAdminLogin) $('error').textContent = '目前登入僅供瀏覽或沒有管理權限，請使用管理員帳號登入。';
      try {
        if (requireAdminLogin) throw new Error('需要管理員重新登入');
        const res = await fetch('/auth/session', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
        const data = await res.json().catch(() => ({}));
        if (res.ok && data.ok) {
          window.location.replace('/main.html');
          return;
        }
      } catch (_) {}
      loadProjects();
      $('loginForm').addEventListener('submit', submitLogin);
    })();
  
