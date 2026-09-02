(async function () {
  const fallback = '/talk.html';
  try {
    const res = await fetch('/auth/web-config', { headers: { 'Accept': 'application/json' } });
    const data = await res.json();
    const entry = typeof data.web_entry_path === 'string' && data.web_entry_path.startsWith('/') && !data.web_entry_path.startsWith('//')
      ? data.web_entry_path : fallback;
    window.location.replace(entry);
  } catch (_) {
    window.location.replace(fallback);
  }
})();
