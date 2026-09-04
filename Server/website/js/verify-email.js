(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ''));
  const challengeID = fragment.get('challenge_id') || '';
  const linkToken = fragment.get('link_token') || '';
  history.replaceState(null, '', window.location.pathname);

  if (!challengeID || !linkToken) {
    $('error').textContent = '臨時驗證連結不完整，請重新提出申請。';
    $('submitBtn').disabled = true;
  }

  $('verifyForm').addEventListener('submit', async (event) => {
    event.preventDefault();
    $('error').textContent = '';
    $('submitBtn').disabled = true;
    try {
      const response = await fetch('/auth/email/complete', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
        body: JSON.stringify({challenge_id: challengeID, link_token: linkToken, code: $('code').value.trim().toUpperCase()})
      });
      const data = await response.json();
      if (!response.ok || data.error || !data.ok) throw new Error(data.error || '驗證失敗');
      $('verifyForm').hidden = true;
      $('message').textContent = data.message || '驗證完成。';
      $('result').hidden = false;
      if (data.auth_token) {
        $('token').value = data.auth_token;
        $('tokenWrap').hidden = false;
      }
    } catch (error) {
      $('error').textContent = error.message || '驗證失敗，請重新確認連結與驗證碼。';
      $('submitBtn').disabled = false;
    }
  });

  $('copyBtn').addEventListener('click', async () => {
    await navigator.clipboard.writeText($('token').value);
    $('copyBtn').textContent = '已複製';
  });
})();
