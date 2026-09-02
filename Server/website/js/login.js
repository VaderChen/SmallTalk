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
          throw new Error(data.error || 'login failed');
        }
        window.location.replace('/main.html');
      } catch (error) {
        $('error').textContent = '請輸入正確帳號密碼';
        $('submitBtn').disabled = false;
      }
    }

    if (getCookie('smalltalk_auth_token')) {
      window.location.replace('/main.html');
    } else {
      loadProjects();
      $('loginForm').addEventListener('submit', submitLogin);
    }
  