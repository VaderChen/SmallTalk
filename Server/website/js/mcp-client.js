(function () {
  'use strict';

  function cookie(name) {
    const prefix = name + '=';
    return document.cookie.split(';').map(v => v.trim()).find(v => v.startsWith(prefix))?.slice(prefix.length) || '';
  }

  class SmallTalkMCPClient {
    constructor(endpoint) {
      this.endpoint = endpoint || window.SMALLTALK_MCP_ENDPOINT || (location.origin ? location.origin + '/mcp' : '/mcp');
      this.sessionID = null;
      this.requestID = 0;
      this.initialized = false;
      this.reconnecting = null;
    }

    resetSession() {
      this.sessionID = null;
      this.initialized = false;
    }

    async request(method, params, notification = false) {
      const headers = { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' };
      if (this.sessionID) headers['Mcp-Session-Id'] = this.sessionID;
      const response = await fetch(this.endpoint, {
        method: 'POST', headers, credentials: 'same-origin', body: JSON.stringify({
          jsonrpc: '2.0', ...(notification ? {} : { id: ++this.requestID }), method, ...(params ? { params } : {})
        })
      });
      const session = response.headers.get('Mcp-Session-Id');
      if (session) this.sessionID = session;
      if (notification && (response.status === 202 || response.status === 204)) return null;
      const text = await response.text();
      let payload;
      try { payload = text ? JSON.parse(text) : {}; } catch (_) {
        if (!response.ok) payload = { error: text || 'MCP HTTP ' + response.status };
        else throw new Error('MCP 回應不是有效 JSON');
      }
      if (!response.ok) {
        const error = new Error(payload.error?.message || payload.error || 'MCP HTTP ' + response.status);
        error.sessionInvalid = Boolean(this.sessionID && [404, 405, 410].includes(response.status));
        throw error;
      }
      if (payload.error) throw new Error(payload.error.message || 'MCP protocol error');
      return payload.result;
    }

    async connect() {
      if (this.initialized) return;
      if (!this.reconnecting) {
        this.reconnecting = (async () => {
          this.resetSession();
          await this.request('initialize', {
            protocolVersion: '2025-03-26', capabilities: {},
            clientInfo: { name: 'SmallTalk Browser UI', version: '1.0.0' }
          });
          await this.request('notifications/initialized', {}, true);
          this.initialized = true;
        })().finally(() => { this.reconnecting = null; });
      }
      await this.reconnecting;
    }

    async call(name, args) {
      await this.connect();
      try {
        const result = await this.request('tools/call', { name, arguments: args || {} });
        if (result?.isError) throw new Error(result.content?.map(c => c.text || '').join('\n') || '工具執行失敗');
        const text = result?.content?.find(c => c.type === 'text')?.text;
        if (text == null) return result;
        try { return JSON.parse(text); } catch (_) { return text; }
      } catch (error) {
        if (!error.sessionInvalid) throw error;
        this.resetSession();
        const result = await this.requestAfterReconnect('tools/call', { name, arguments: args || {} });
        if (result?.isError) throw new Error(result.content?.map(c => c.text || '').join('\n') || '工具執行失敗');
        const text = result?.content?.find(c => c.type === 'text')?.text;
        if (text == null) return result;
        try { return JSON.parse(text); } catch (_) { return text; }
      }
    }

    async requestAfterReconnect(method, params) {
      await this.connect();
      return this.request(method, params);
    }

    async listTools() { await this.connect(); return this.request('tools/list', {}); }
  }

  window.SmallTalkMCPClient = SmallTalkMCPClient;
})();
