(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);

  async function complete() {
    const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ''));
    const challengeID = fragment.get('challenge_id') || '';
    const agentToken = fragment.get('agent_token') || '';
    history.replaceState(null, '', window.location.pathname);
    if (!challengeID || !agentToken) throw new Error('Agent 自動驗證 URL 不完整，請重新提出申請。');

    const response = await fetch('/auth/email/agent-complete', {
      method: 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
      body: JSON.stringify({challenge_id: challengeID, agent_token: agentToken})
    });
    const data = await response.json();
    if (!response.ok || data.error || !data.ok) throw new Error(data.error || 'Agent 自動驗證失敗');

    $('status').textContent = '已完成';
    $('message').textContent = data.message || '驗證完成。';
    $('response').value = JSON.stringify(data, null, 2);
    $('result').hidden = false;
    if (data.auth_token) {
      $('token').value = data.auth_token;
      $('tokenWrap').hidden = false;
    }
  }

  $('copyBtn').addEventListener('click', async () => {
    await navigator.clipboard.writeText($('token').value);
    $('copyBtn').textContent = '已複製';
  });

  complete().catch((error) => {
    $('status').textContent = '失敗';
    $('error').textContent = error.message || 'Agent 自動驗證失敗。';
  });
})();
