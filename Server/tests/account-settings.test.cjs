const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

// 不連網站；以失效認證與延遲回應驗證對話框生命週期。
function harness(call) {
  const elements = new Map();
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      id, hidden: false, disabled: false, open: false, value: '', textContent: '', children: [], events: {},
      addEventListener(type, fn) { this.events[type] = fn; },
      replaceChildren(...items) { this.children = items; },
      append(...items) { this.children.push(...items); },
      querySelectorAll() { return []; },
      showModal() { this.open = true; },
      close() { this.open = false; this.events.close?.(); }
    });
    return elements.get(id);
  }
  const context = { document: { getElementById: element, createElement: () => element(Symbol()), cookie: '' },
    window: {}, SmallTalkMCPClient: class { call(...args) { return call(...args); } resetSession() {} },
    location: { origin: 'http://localhost', protocol: 'http:' }, navigator: {}, setTimeout, clearTimeout,
    fetch: async () => ({ ok: true, json: async () => ({status:'idle'}) }) };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../website/js/account-settings.js'), 'utf8'), context);
  return { element, open: context.window.openAccountSettings, setFetch: fn => {context.fetch=fn;} };
}
const profile = { client_id:'test-account', display_name:'本機測資', can_rename:true, name_history:[] };
test('改名失敗且認證失效時，清除舊資料且不重新啟用修改按鈕', async () => {
  let failed = false;
  const ui = harness(async name => {
    if (name === 'smalltalk_auth_status') { if (failed) throw Error('expired'); return {authenticated:true,account_approved:true}; }
    if (name === 'smalltalk_account_profile') return profile;
    failed = true; throw Error('save failed');
  });
  await ui.open(); assert.equal(ui.element('accountRenameSubmit').disabled, false);
  ui.element('accountName').value = '新名稱';
  await ui.element('accountRenameForm').events.submit({preventDefault(){}});
  assert.equal(ui.element('accountRenameSubmit').disabled, true);
  assert.equal(ui.element('accountRenameForm').hidden, true);
  assert.equal(ui.element('accountInfo').children.length, 0);
});
test('關閉對話框後才到達的改名回應不恢復帳號資料或啟用按鈕', async () => {
  let finish;
  const ui = harness(async name => {
    if (name === 'smalltalk_auth_status') return {authenticated:true,account_approved:true};
    if (name === 'smalltalk_account_profile') return profile;
    return new Promise(resolve => { finish = resolve; });
  });
  await ui.open(); ui.element('accountName').value = '新名稱';
  const saving = ui.element('accountRenameForm').events.submit({preventDefault(){}});
  ui.element('dlgAccount').close(); finish({...profile,display_name:'新名稱'}); await saving;
  assert.equal(ui.element('accountInfo').children.length, 0);
  assert.equal(ui.element('accountRenameSubmit').disabled, true);
});
test('重新開啟帳號設定會恢復原請求並顯示可複製連結', async () => {
  const ui = harness(async () => ({authenticated:false}));
  // 此案例由 harness 提供已存在的瀏覽器請求回應。
  ui.setFetch(async () => ({ok:true,json:async()=>({status:'pending',request_id:'existing-request'})}));
  await ui.open();
  assert.equal(ui.element('accountApprovalURL').value, 'http://localhost/agent-view.html#request=existing-request');
  assert.equal(ui.element('accountApproval').hidden, false);
  assert.match(ui.element('accountViewStatus').textContent, /已接續/);
  ui.element('dlgAccount').close();
});
