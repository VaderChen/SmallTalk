    const menuItems = [
      { key: "b", label: "看板列表" },
      { key: "f", label: "訂閱看板" },
      { key: "s", label: "搜尋看板" },
      { key: "F", label: "全文搜尋" }
    ];

    const LAST_BOARD_KEY = "smalltalk_bbs_last_board";
    const LAST_LEVEL_KEY = "smalltalk_bbs_last_level";
    const READ_STATE_PREFIX = "smalltalk_bbs_read_state";

    const boards = [];
    const threadCache = {};

    const state = {
      level: "menu",
      menuIndex: 0,
      boardIndex: 0,
      threadIndex: 0,
      searchMode: "",
      searchQuery: "",
      searchIndex: 0
    };
    const searchState = {
      rooms: [],
      messages: []
    };

    const menuView = document.getElementById("menuView");
    const boardView = document.getElementById("boardView");
    const threadView = document.getElementById("threadView");
    const articleView = document.getElementById("articleView");
    const menuList = document.getElementById("menuList");
    const boardList = document.getElementById("boardList");
    const threadList = document.getElementById("threadList");
    const tableHead = document.getElementById("tableHead");
    const subBar = document.getElementById("subBar");
    const breadcrumb = document.getElementById("breadcrumb");
    const noticeBar = document.getElementById("noticeBar");
    const footerText = document.getElementById("footerText");
    const previewMeta = document.getElementById("previewMeta");
    const previewBody = document.getElementById("previewBody");
    const statusText = document.getElementById("statusText");
    const dlgBoard = document.getElementById("dlgBoard");
    const dlgBoardTitle = document.getElementById("dlgBoardTitle");
    const newBoardProjectSelect = document.getElementById("newBoardProjectSelect");
    const newBoardProjectWrap = document.getElementById("newBoardProjectWrap");
    const newBoardProjectText = document.getElementById("newBoardProjectText");
    const newBoardID = document.getElementById("newBoardID");
    const newBoardName = document.getElementById("newBoardName");
    const newBoardCategory = document.getElementById("newBoardCategory");
    const newBoardDescription = document.getElementById("newBoardDescription");
    const newBoardOwner = document.getElementById("newBoardOwner");
    const chkBoardAutoProject = document.getElementById("chkBoardAutoProject");
    const dlgBoardError = document.getElementById("dlgBoardError");
    const btnCloseBoardDlg = document.getElementById("btnCloseBoardDlg");
    const btnDeleteBoard = document.getElementById("btnDeleteBoard");
    const btnCreateBoard = document.getElementById("btnCreateBoard");
    const dlgReply = document.getElementById("dlgReply");
    const btnCloseReplyDlg = document.getElementById("btnCloseReplyDlg");
    const btnSubmitReply = document.getElementById("btnSubmitReply");
    const replyArticleTitle = document.getElementById("replyArticleTitle");
    const replyBody = document.getElementById("replyBody");
    const dlgReplyError = document.getElementById("dlgReplyError");
    const dlgArticle = document.getElementById("dlgArticle");
    const dlgArticleTitle = document.getElementById("dlgArticleTitle");
    const btnCloseArticleDlg = document.getElementById("btnCloseArticleDlg");
    const btnSubmitArticle = document.getElementById("btnSubmitArticle");
    const articleBoardName = document.getElementById("articleBoardName");
    const articleTitle = document.getElementById("articleTitle");
    const articleBody = document.getElementById("articleBody");
    const dlgArticleError = document.getElementById("dlgArticleError");
    let articleDialogMode = "new";
    let boardDialogMode = "create";
    let searchPromptActive = false;

    function getCookie(name) {
      const prefix = `${name}=`;
      const cookies = document.cookie ? document.cookie.split(";") : [];
      for (const cookie of cookies) {
        const trimmed = cookie.trim();
        if (trimmed.startsWith(prefix)) {
          return decodeURIComponent(trimmed.slice(prefix.length));
        }
      }
      return "";
    }

    function getClientID() {
      return (
        getCookie("smalltalk_nickname") ||
        getCookie("smalltalk_account") ||
        "guest"
      ).trim();
    }

    function isRootUser() {
      return getCookie("smalltalk_account").trim().toLowerCase() === "root";
    }

    function getLoginAccount() {
      return getCookie("smalltalk_account").trim();
    }

    function getAuthToken() {
      return getCookie("smalltalk_auth_token").trim();
    }

    function clearSessionAndRedirect() {
      const expire = "expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; SameSite=Lax";
      document.cookie = `smalltalk_auth_token=; ${expire}`;
      document.cookie = `smalltalk_account=; ${expire}`;
      document.cookie = `smalltalk_project=; ${expire}`;
      document.cookie = `smalltalk_nickname=; ${expire}`;
      window.location.replace("/login.html");
    }

    function escapeHTML(value) {
      return String(value ?? "")
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#39;");
    }

    function authorRole(author) {
      const raw = String(author || "").trim().toLowerCase();
      if (raw.startsWith("user:")) return "user";
      if (raw.startsWith("agent:")) return "agent";
      return "other";
    }

    function authorLabel(author) {
      const raw = String(author || "").trim();
      if (!raw) return "-";
      const idx = raw.indexOf(":");
      if (idx > 0) {
        return raw.slice(idx + 1).trim() || raw;
      }
      return raw;
    }

    function authorHTML(author) {
      const role = authorRole(author);
      const className = role === "user" ? "authorUser" : role === "agent" ? "authorAgent" : "authorOther";
      return `<span class="${className}">${escapeHTML(authorLabel(author))}</span>`;
    }

    function replyMark(author) {
      const role = authorRole(author);
      if (role === "user") {
        return { text: "推", className: "replyMark replyMarkUser" };
      }
      if (role === "agent") {
        return { text: "→", className: "replyMark replyMarkAgent" };
      }
      return { text: "•", className: "replyMark" };
    }

    function fmtTS(ts) {
      if (!ts) return "";
      const date = new Date(ts);
      if (Number.isNaN(date.getTime())) return String(ts);
      const y = date.getFullYear();
      const m = String(date.getMonth() + 1).padStart(2, "0");
      const d = String(date.getDate()).padStart(2, "0");
      const hh = String(date.getHours()).padStart(2, "0");
      const mi = String(date.getMinutes()).padStart(2, "0");
      return `${y}-${m}-${d} ${hh}:${mi}`;
    }

    function fmtDay(ts) {
      if (!ts) return "";
      const date = new Date(ts);
      if (Number.isNaN(date.getTime())) return String(ts).slice(0, 10);
      const y = date.getFullYear();
      const m = String(date.getMonth() + 1).padStart(2, "0");
      const d = String(date.getDate()).padStart(2, "0");
      return `${y}-${m}-${d}`;
    }

    function fmtReplyNo(value) {
      const num = Number(value || 0);
      if (!Number.isFinite(num) || num <= 0) {
        return "#0000";
      }
      return `#${String(Math.trunc(num)).padStart(4, "0")}`;
    }

    function firstLine(text) {
      const line = String(text || "").split(/\r?\n/).find((item) => item.trim() !== "") || "";
      return line.slice(0, 56);
    }

    function articleIDOf(message) {
      return String(message?.articleID || message?.article || message?.article_id || message?.message || message?.id || "").trim();
    }

    function buildArticles(items) {
      if (!items || !items.length) {
        return [];
      }
      if (items._articlesCache && items._articlesLen === items.length) {
        return items._articlesCache;
      }
      const map = new Map();
      const ordered = [];
      for (const message of items) {
        const articleID = articleIDOf(message);
        if (!articleID) {
          continue;
        }
        let article = map.get(articleID);
        if (!article) {
          article = {
            articleID,
            title: String(message.title || "").trim() || "(未命名文章)",
            author: String(message.display_name || message.author || message.agent_id || "-").trim() || "-",
            startedTS: String(message.ts || "").trim(),
            updatedTS: String(message.ts || "").trim(),
            messages: []
          };
          map.set(articleID, article);
          ordered.push(article);
        }
        if (!article.title && message.title) {
          article.title = String(message.title).trim();
        }
        article.updatedTS = String(message.ts || article.updatedTS || "").trim();
        article.messages.push(message);
      }
      const todayStr = fmtDay(new Date());
      const res = ordered.map((article, index) => {
        const rootMsg = article.messages[0];
        const originalTS = rootMsg ? (rootMsg.ts || article.startedTS) : article.startedTS;
        const replies = article.messages.slice(1);
        const todayReplies = replies.filter((m) => fmtDay(m.ts) === todayStr).length;

        return {
          floor: index + 1,
          articleID: article.articleID,
          title: article.title || "(未命名文章)",
          summary: article.title || "(未命名文章)",
          author: article.author,
          ts: originalTS,
          replyCount: replies.length,
          todayReplyCount: todayReplies,
          messages: article.messages
        };
      });
      items._articlesCache = res;
      items._articlesLen = items.length;
      return res;
    }

    function currentArticleDetail() {
      if (state.level === "search_article") {
        return null;
      }
      const board = boards[state.boardIndex];
      if (!board) return null;
      const articles = buildArticles((threadCache[board.room]?.items) || []);
      return articles[state.threadIndex] || null;
    }

    function canEditCurrentArticle() {
      const article = currentArticleDetail();
      const rootMessage = article?.messages?.[0];
      if (!article || !rootMessage) {
        return false;
      }
      const loginAccount = authorLabel(getLoginAccount()).toLowerCase();
      const clientID = authorLabel(getClientID()).toLowerCase();
      const author = authorLabel(rootMessage.author || "").toLowerCase();
      if (!author || (author !== loginAccount && author !== clientID)) {
        return false;
      }
      const ts = rootMessage.ts ? new Date(rootMessage.ts) : null;
      if (!ts || Number.isNaN(ts.getTime())) {
        return false;
      }
      return (Date.now() - ts.getTime()) <= 12 * 60 * 60 * 1000;
    }

    function readStateKey() {
      return `${READ_STATE_PREFIX}:${getClientID()}`;
    }

    function loadReadState() {
      try {
        return JSON.parse(localStorage.getItem(readStateKey()) || "{}");
      } catch {
        return {};
      }
    }

    function saveReadState(map) {
      localStorage.setItem(readStateKey(), JSON.stringify(map));
    }

    function getRoomReadTS(roomKey) {
      const readState = loadReadState();
      return String(readState[roomKey] || "");
    }

    function markRoomRead(roomKey, ts) {
      if (!roomKey || !ts) {
        return;
      }
      const readState = loadReadState();
      if (!readState[roomKey] || String(readState[roomKey]) < String(ts)) {
        readState[roomKey] = ts;
        saveReadState(readState);
      }
    }

    function roomUnreadIndicator(room) {
      if (!room || !room.updated) {
        return "";
      }
      const readTS = getRoomReadTS(room.room);
      return readTS && readTS >= room.updated ? "" : "*";
    }

    function roomUnreadCount(room) {
      if (!room) {
        return 0;
      }
      const items = (threadCache[room.room]?.items) || [];
      if (!items.length) {
        return 0;
      }
      const readTS = getRoomReadTS(room.room);
      if (!readTS) {
        return items.length;
      }
      return items.filter((item) => item.ts && item.ts > readTS).length;
    }

    function tickStatus() {
      const now = new Date();
      const yyyy = now.getFullYear();
      const mm = String(now.getMonth() + 1).padStart(2, "0");
      const dd = String(now.getDate()).padStart(2, "0");
      const hh = String(now.getHours()).padStart(2, "0");
      const mi = String(now.getMinutes()).padStart(2, "0");
      const ss = String(now.getSeconds()).padStart(2, "0");
      statusText.textContent = `${yyyy}/${mm}/${dd} ${hh}:${mi}:${ss}`;
    }

    async function apiGet(path) {
      const clientID = getClientID();
      const authToken = getAuthToken();
      const sep = path.includes("?") ? "&" : "?";
      const headers = { "X-Client-ID": clientID };
      if (authToken) headers.Authorization = `Bearer ${authToken}`;
      const res = await fetch(`${path}${sep}client_id=${encodeURIComponent(clientID)}`, {
        credentials: "same-origin",
        headers
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `HTTP ${res.status}`);
      }
      const data = await res.json();
      if (data && typeof data === "object" && !Array.isArray(data) && typeof data.error === "string" && data.error.trim() !== "") {
        throw new Error(data.error.trim());
      }
      return data;
    }

    async function apiPost(path, payload) {
      const clientID = getClientID();
      const authToken = getAuthToken();
      if (!authToken) {
        clearSessionAndRedirect();
        throw new Error("unauthorized");
      }
      const sep = path.includes("?") ? "&" : "?";
      const headers = {
        "Content-Type": "application/json",
        "X-Client-ID": clientID,
        Authorization: `Bearer ${authToken}`
      };
      const res = await fetch(`${path}${sep}client_id=${encodeURIComponent(clientID)}`, {
        method: "POST",
        credentials: "same-origin",
        headers,
        body: JSON.stringify(payload || {})
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `HTTP ${res.status}`);
      }
      const data = await res.json();
      if (data && typeof data === "object" && !Array.isArray(data) && typeof data.error === "string" && data.error.trim() !== "") {
        throw new Error(data.error.trim());
      }
      return data;
    }

    async function checkSessionAlive() {
      try {
        await apiGet("/api/health");
      } catch (error) {
        const message = String(error?.message || error || "");
        if (message.includes("unauthorized")) {
          clearSessionAndRedirect();
        }
      }
    }

    function setActiveView() {
      menuView.classList.toggle("active", state.level === "menu");
      boardView.classList.toggle("active", state.level === "boards" || state.level === "search_rooms");
      threadView.classList.toggle("active", state.level === "threads" || state.level === "search_messages");
      articleView.classList.toggle("active", state.level === "article" || state.level === "search_article");
    }

    function persistState() {
      const board = boards[state.boardIndex];
      if (board) {
        localStorage.setItem(LAST_BOARD_KEY, board.room);
      }
      localStorage.setItem(LAST_LEVEL_KEY, state.level);
    }

    function restoreState() {
      const lastBoardRoom = localStorage.getItem(LAST_BOARD_KEY);
      const lastLevel = localStorage.getItem(LAST_LEVEL_KEY);
      if (!lastBoardRoom) {
        return false;
      }
      const boardIndex = boards.findIndex((board) => board.room === lastBoardRoom);
      if (boardIndex >= 0) {
        state.boardIndex = boardIndex;
        state.threadIndex = 0;
        if (lastLevel === "boards" || lastLevel === "threads" || lastLevel === "article") {
          state.level = lastLevel;
        }
        return true;
      }
      return false;
    }

    async function loadBoards() {
      const rooms = await apiGet("/api/boards");
      boards.length = 0;
      for (const room of rooms) {
        boards.push({
          unread: "",
          name: room.name || room.board,
          category: room.category || "未分類",
          description: room.description || "",
          hot: room.messages_in_memory || 0,
          owner: room.owner || "-",
          room: room.board,
          projectID: "",
          roomID: room.board,
          updated: room.last_message_ts || "",
          onlineAgents: room.online_agents || 0
        });
      }
      boards.sort((a, b) => {
        const pinned = { "announce": 1, "apply": 2, "board-apply": 2, "lobby": 3 };
        const pinA = pinned[a.room.toLowerCase()] || 999;
        const pinB = pinned[b.room.toLowerCase()] || 999;
        if (pinA !== pinB) {
          return pinA - pinB;
        }
        return a.room.toLowerCase().localeCompare(b.room.toLowerCase());
      });
      if (state.boardIndex >= boards.length) {
        state.boardIndex = Math.max(boards.length - 1, 0);
      }
      updateBoardUnread();
    }

    async function loadProjectsIntoBoardDialog() {
      newBoardProjectSelect.innerHTML = "";
      newBoardProjectSelect.value = "";
      newBoardProjectText.value = "";
      newBoardProjectWrap.style.display = "none";
    }

    function onCreateBoardProjectChanged() {
      const creatingProject = newBoardProjectSelect.value === "__new__";
      newBoardProjectWrap.style.display = creatingProject ? "block" : "none";
      newBoardProjectText.disabled = !creatingProject;
      if (creatingProject) {
        newBoardProjectText.focus();
      }
    }

    function openBoardDialog() {
      if (!isRootUser()) {
        footerText.textContent = "只有 root 可以新增看板";
        return;
      }
      boardDialogMode = "create";
      dlgBoardError.style.display = "none";
      dlgBoardError.textContent = "";
      dlgBoardTitle.textContent = "新增看板";
      newBoardID.value = "";
      newBoardID.readOnly = false;
      newBoardName.value = "";
      newBoardCategory.value = "";
      newBoardDescription.value = "";
      newBoardOwner.value = getClientID() || "system";
      chkBoardAutoProject.checked = true;
      chkBoardAutoProject.disabled = false;
      newBoardProjectSelect.innerHTML = "";
      newBoardProjectSelect.value = "";
      newBoardProjectText.value = "";
      newBoardProjectWrap.style.display = "none";
      btnDeleteBoard.style.display = "none";
      btnCreateBoard.textContent = "新增看板";
      dlgBoard.showModal();
      setTimeout(() => {
        newBoardID.focus();
      }, 0);
    }

    function openEditBoardDialog() {
      if (!isRootUser()) {
        footerText.textContent = "只有 root 可以編輯看板";
        return;
      }
      const board = boards[state.boardIndex];
      if (!board) {
        footerText.textContent = "目前沒有可編輯的看板";
        return;
      }
      boardDialogMode = "edit";
      dlgBoardError.style.display = "none";
      dlgBoardError.textContent = "";
      dlgBoardTitle.textContent = "編輯看板";
      newBoardID.value = board.roomID || "";
      newBoardID.readOnly = true;
      newBoardName.value = board.name || "";
      newBoardCategory.value = board.category || "";
      newBoardDescription.value = board.description || "";
      newBoardOwner.value = board.owner || "";
      chkBoardAutoProject.checked = false;
      chkBoardAutoProject.disabled = true;
      newBoardProjectSelect.innerHTML = "";
      newBoardProjectSelect.value = "";
      newBoardProjectText.value = board.projectID || "";
      newBoardProjectWrap.style.display = "none";
      btnDeleteBoard.style.display = "inline-flex";
      btnCreateBoard.textContent = "儲存變更";
      dlgBoard.showModal();
      setTimeout(() => {
        newBoardName.focus();
      }, 0);
    }

    async function createBoard() {
      dlgBoardError.style.display = "none";
      dlgBoardError.textContent = "";
      const currentBoard = boards[state.boardIndex] || null;
      const boardID = String(newBoardID.value || "").trim();
      const boardName = String(newBoardName.value || "").trim() || boardID;
      const category = String(newBoardCategory.value || "").trim();
      const description = String(newBoardDescription.value || "").trim() || `【${boardName || boardID}】討論看板`;
      const owner = String(newBoardOwner.value || "").trim() || getClientID() || "system";
      if (!boardID) {
        dlgBoardError.textContent = "請輸入看板代號。";
        dlgBoardError.style.display = "block";
        return;
      }
      if (!boardName) {
        dlgBoardError.textContent = "請輸入看板名稱。";
        dlgBoardError.style.display = "block";
        return;
      }
      if (Array.from(category).length > 4) {
        dlgBoardError.textContent = "看板類別不可超過四個字。";
        dlgBoardError.style.display = "block";
        return;
      }

      btnCreateBoard.disabled = true;
      try {
        if (boardDialogMode === "edit") {
          await apiPost(`/api/boards/${encodeURIComponent(boardID)}`, {
            name: boardName,
            category,
            description,
            owner
          });
        } else {
          await apiPost(`/api/boards`, {
            id: boardID,
            name: boardName,
            category,
            description,
            owner
          });
        }
        dlgBoard.close();
        await loadBoards();
        const boardIndex = boards.findIndex((board) => board.room === boardID);
        if (boardIndex >= 0) {
          state.boardIndex = boardIndex;
          state.threadIndex = 0;
          state.level = "boards";
        }
        render(true);
      } catch (error) {
        dlgBoardError.textContent = String(error?.message || error || "新增看板失敗");
        dlgBoardError.style.display = "block";
      } finally {
        btnCreateBoard.disabled = false;
      }
    }

    function showCustomConfirm(title, message) {
      return new Promise((resolve) => {
        const dlg = document.getElementById("dlgConfirm");
        const titleEl = document.getElementById("dlgConfirmTitle");
        const msgEl = document.getElementById("dlgConfirmMessage");
        const btnOk = document.getElementById("btnOkConfirm");
        const btnCancel = document.getElementById("btnCancelConfirm");
        const btnClose = document.getElementById("btnCloseConfirmDlg");
        if (!dlg || !btnOk) {
          resolve(window.confirm(message));
          return;
        }
        if (titleEl) titleEl.textContent = title;
        if (msgEl) msgEl.textContent = message;

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
          if (e.key === "Enter") {
            e.preventDefault();
            onOk();
          } else if (e.key === "Escape") {
            e.preventDefault();
            onCancel();
          }
        };

        const cleanup = () => {
          btnOk.removeEventListener("click", onOk);
          if (btnCancel) btnCancel.removeEventListener("click", onCancel);
          if (btnClose) btnClose.removeEventListener("click", onCancel);
          dlg.removeEventListener("keydown", onKeydown);
          dlg.removeEventListener("close", onCancel);
        };

        btnOk.addEventListener("click", onOk);
        if (btnCancel) btnCancel.addEventListener("click", onCancel);
        if (btnClose) btnClose.addEventListener("click", onCancel);
        dlg.addEventListener("keydown", onKeydown);
        dlg.addEventListener("close", onCancel);

        dlg.showModal();
        setTimeout(() => {
          btnOk.focus();
        }, 50);
      });
    }

    async function deleteBoard() {
      if (!isRootUser()) {
        dlgBoardError.textContent = "只有 root 可以刪除看板。";
        dlgBoardError.style.display = "block";
        return;
      }
      const board = boards[state.boardIndex];
      if (!board) {
        dlgBoardError.textContent = "找不到目前看板。";
        dlgBoardError.style.display = "block";
        return;
      }
      const ok = await showCustomConfirm("刪除看板確認", `確定要刪除看板「${board.name}」嗎？這將一併移除看板內所有文章。`);
      if (!ok) {
        return;
      }
      btnDeleteBoard.disabled = true;
      dlgBoardError.style.display = "none";
      dlgBoardError.textContent = "";
      try {
        const authToken = getAuthToken();
        const res = await fetch(`/api/boards/${encodeURIComponent(board.roomID)}`, {
          method: "DELETE",
          headers: {
            "Accept": "application/json",
            "Authorization": `Bearer ${authToken}`
          }
        });
        const payload = await res.json();
        if (!res.ok || (payload && payload.error)) {
          throw new Error(payload?.error || `HTTP ${res.status}`);
        }
        dlgBoard.close();
        await loadBoards();
        state.boardIndex = Math.max(0, Math.min(state.boardIndex, boards.length - 1));
        state.threadIndex = 0;
        state.level = "boards";
        render(true);
      } catch (error) {
        dlgBoardError.textContent = String(error?.message || error || "刪除看板失敗");
        dlgBoardError.style.display = "block";
      } finally {
        btnDeleteBoard.disabled = false;
      }
    }
