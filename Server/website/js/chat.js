(() => {
  const files = [
    "/js/chat.core.js",
    "/js/chat.data.js",
    "/js/chat.render.js",
    "/js/chat.actions.js",
    "/js/chat.bootstrap.js"
  ];
  if (window.__smallTalkChatCompatLoader) {
    return;
  }
  window.__smallTalkChatCompatLoader = true;
  const current = document.currentScript;
  if (current && current.dataset.splitLoaded === "true") {
    return;
  }
  files.forEach((src) => {
    const script = document.createElement("script");
    script.src = src;
    script.dataset.splitLoaded = "true";
    document.body.appendChild(script);
  });
})();

    async function runRoomSearch(query) {
      const data = await apiGet(`/api/search/rooms?q=${encodeURIComponent(query)}&limit=80`);
      searchState.rooms = Array.isArray(data.rooms) ? data.rooms.map((room) => ({
        unread: "",
        name: room.name || room.room_id,
        category: room.category || "未分類",
        description: room.description || "",
        hot: room.messages_in_memory || 0,
        owner: room.owner || "-",
        room: `${room.project_id}/${room.room_id}`,
        projectID: room.project_id,
        roomID: room.room_id,
        updated: room.last_message_ts || "",
        onlineAgents: room.online_agents || 0
      })) : [];
      state.searchMode = "rooms";
      state.searchQuery = query;
      state.searchIndex = 0;
      state.level = "search_rooms";
    }

    async function runMessageSearch(query) {
      const data = await apiGet(`/api/search/messages?q=${encodeURIComponent(query)}&limit=120`);
      searchState.messages = Array.isArray(data.messages) ? data.messages.map((hit, index) => ({
        floor: index + 1,
        projectID: hit.project_id || "",
        roomID: hit.room_id || "",
        roomName: hit.room_name || hit.room_id || "",
        room: `${hit.project_id}/${hit.room_id}`,
        id: hit.message?.id || "",
        author: hit.message?.display_name || hit.message?.author || hit.message?.agent_id || "-",
        articleID: hit.message?.article_id || hit.message?.id || "",
        title: hit.message?.title || "",
        replyToMessageID: hit.message?.reply_to_message_id || "",
        ts: hit.message?.ts || "",
        summary: hit.message?.title || "(未命名文章)",
        body: hit.message?.text || ""
      })) : [];
      state.searchMode = "messages";
      state.searchQuery = query;
      state.searchIndex = 0;
      state.level = "search_messages";
    }

    function promptSearch(kind) {
      if (searchPromptActive) {
        return Promise.resolve();
      }
      searchPromptActive = true;
      return new Promise((resolve) => {
        const dlg = document.getElementById("dlgSearch");
        const titleEl = document.getElementById("dlgSearchTitle");
        const labelEl = document.getElementById("dlgSearchLabel");
        const inputEl = document.getElementById("searchKeyword");
        const btnSubmit = document.getElementById("btnSubmitSearch");
        const btnClose = document.getElementById("btnCloseSearchDlg");
        if (!dlg || !inputEl || !btnSubmit) {
          searchPromptActive = false;
          resolve();
          return;
        }

        const label = kind === "rooms" ? "搜尋看板" : "全文搜尋";
        if (titleEl) titleEl.textContent = label;
        if (labelEl) labelEl.textContent = kind === "rooms" ? "看板關鍵字" : "全文關鍵字";
        inputEl.value = state.searchQuery || "";

        let done = false;
        const finish = () => {
          if (done) return;
          done = true;
          searchPromptActive = false;
          cleanup();
          try { dlg.close(); } catch (_) {}
          resolve();
        };

        const onSubmit = async () => {
          const trimmed = inputEl.value.trim();
          finish();
          if (!trimmed) return;
          try {
            if (kind === "rooms") {
              await runRoomSearch(trimmed);
            } else {
              await runMessageSearch(trimmed);
            }
            render();
          } catch (error) {
            console.error(error);
            previewMeta.textContent = "搜尋失敗";
            previewBody.textContent = String(error.message || error);
          }
        };

        const onCancel = () => {
          finish();
        };

        const onKeydown = (e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            onSubmit();
          } else if (e.key === "Escape") {
            e.preventDefault();
            onCancel();
          }
        };

        const cleanup = () => {
          btnSubmit.removeEventListener("click", onSubmit);
          if (btnClose) btnClose.removeEventListener("click", onCancel);
          inputEl.removeEventListener("keydown", onKeydown);
          dlg.removeEventListener("close", onCancel);
        };

        btnSubmit.addEventListener("click", onSubmit);
        if (btnClose) btnClose.addEventListener("click", onCancel);
        inputEl.addEventListener("keydown", onKeydown);
        dlg.addEventListener("close", onCancel);

        dlg.showModal();
        setTimeout(() => {
          inputEl.focus();
          inputEl.select();
        }, 50);
      });
    }

    async function ensureThreadsLoaded() {
      const board = boards[state.boardIndex];
      if (!board) {
        return { items: [], hasMore: false, nextBeforeID: "" };
      }
      if (threadCache[board.room]) {
        return threadCache[board.room];
      }
      const data = await apiGet(`/api/projects/${encodeURIComponent(board.projectID)}/rooms/${encodeURIComponent(board.roomID)}/messages?limit=80`);
      const items = (Array.isArray(data.messages) ? data.messages : []).map((message, index) => ({
        floor: index + 1,
        id: message.id || "",
        author: message.display_name || message.author || message.agent_id || "-",
        articleID: message.article_id || message.id || "",
        title: message.title || "",
        replyToMessageID: message.reply_to_message_id || "",
        ts: message.ts || "",
        summary: message.title || "(未命名文章)",
        body: message.text || ""
      }));
      threadCache[board.room] = {
        items,
        hasMore: !!data.has_more,
        nextBeforeID: data.next_before_id || "",
        nextBeforeTS: data.next_before_ts || ""
      };
      updateBoardUnread();
      return threadCache[board.room];
    }

    function updateBoardUnread() {
      boards.forEach((board) => {
        const count = roomUnreadCount(board);
        board.unread = count > 0 ? String(count) : roomUnreadIndicator(board);
      });
    }

    function renderMenu() {
      menuList.innerHTML = "";
      menuItems.forEach((item, index) => {
        const row = document.createElement("div");
        row.className = "menuRow" + (state.level === "menu" && state.menuIndex === index ? " activeRow" : "");
        row.innerHTML = `
          <div class="menuKey">${item.key})</div>
          <div>${escapeHTML(item.label)}</div>
        `;
        row.addEventListener("click", async () => {
          state.menuIndex = index;
          await enterNextLevel();
        });
        menuList.appendChild(row);
      });
    }

    function renderBoards() {
      const source = state.level === "search_rooms" ? searchState.rooms : boards;
      updateBoardUnread();
      boardList.innerHTML = "";
      source.forEach((board, index) => {
        const row = document.createElement("div");
        const active = (state.level === "boards" && state.boardIndex === index) || (state.level === "search_rooms" && state.searchIndex === index);
        row.className = "boardRow" + (active ? " activeRow" : "");
        row.innerHTML = `
          <div class="boardCursorCol"></div>
          <div class="left">${index + 1}</div>
          <div class="left">${escapeHTML(board.unread)}</div>
          <div class="boardName">${escapeHTML(board.name)}</div>
          <div>${escapeHTML(board.category)}</div>
          <div class="boardDesc">${escapeHTML(board.description || board.room)}</div>
          <div class="boardHot left">${board.hot}</div>
          <div class="boardOwner">${escapeHTML(board.owner)}</div>
        `;
        row.addEventListener("click", async () => {
          if (state.level === "search_rooms") {
            state.searchIndex = index;
          } else {
            state.boardIndex = index;
            state.threadIndex = 0;
          }
          await enterNextLevel();
        });
        boardList.appendChild(row);
      });
    }

    function renderThreads() {
      threadList.innerHTML = "";
      if (state.level === "search_messages") {
        const items = searchState.messages;
        items.forEach((thread, index) => {
          const row = document.createElement("div");
          row.className = "threadRow" + (state.searchIndex === index ? " activeRow" : "");
          row.innerHTML = `
            <div class="boardCursorCol"></div>
            <div class="threadFloor">#${thread.floor}</div>
            <div class="threadDate">${escapeHTML(fmtDay(thread.ts))}</div>
            <div class="threadAuthor">${authorHTML(thread.author)}</div>
            <div class="threadTitle"><span class="articleMark">■</span><span class="threadTitleText">${escapeHTML(thread.title || `${thread.roomName} · ${thread.summary}`)}</span></div>
            <div class="threadReplies">-</div>
          `;
          row.addEventListener("click", async () => {
            state.searchIndex = index;
            await enterNextLevel();
          });
          threadList.appendChild(row);
        });
        return;
      }

      const board = boards[state.boardIndex];
      const page = board ? threadCache[board.room] : null;
      const articles = buildArticles(page ? page.items : []);
      if (!board) {
        return;
      }

      articles.forEach((thread, index) => {
        const row = document.createElement("div");
        row.className = "threadRow" + (state.level === "threads" && state.threadIndex === index ? " activeRow" : "");
        row.innerHTML = `
          <div class="boardCursorCol"></div>
          <div class="threadFloor">#${thread.floor}</div>
          <div class="threadDate">${escapeHTML(fmtDay(thread.ts))}</div>
          <div class="threadAuthor">${authorHTML(thread.author)}</div>
          <div class="threadTitle"><span class="articleMark">■</span><span class="threadTitleText">${escapeHTML(thread.title || "(未命名文章)")}</span></div>
          <div class="threadReplies">${thread.replyCount}</div>
        `;
        row.addEventListener("click", async () => {
          state.threadIndex = index;
          await enterNextLevel();
        });
        threadList.appendChild(row);
      });
    }

    function renderArticle() {
      previewMeta.textContent = "";
      previewBody.innerHTML = "";

      if (state.level === "search_article") {
        const current = searchState.messages[state.searchIndex];
        if (!current) {
          previewMeta.textContent = "全文搜尋 / 無結果";
          return;
        }
        previewBody.innerHTML = `
          <div class="articleShell">
            <div class="articleHeader">
              <div class="articleHeaderRow">
                <div class="articleHeaderKey">作者</div>
                <div class="articleHeaderValue">${authorHTML(current.author)}</div>
                <div class="articleBoardTag">看板 ${escapeHTML(current.roomName)}</div>
              </div>
              <div class="articleHeaderRow">
                <div class="articleHeaderKey">標題</div>
                <div class="articleHeaderValue">${escapeHTML(current.title || "(未命名文章)")}</div>
              </div>
              <div class="articleHeaderRow">
                <div class="articleHeaderKey">時間</div>
                <div class="articleHeaderValue">${escapeHTML(fmtTS(current.ts))}</div>
              </div>
            </div>
            <div class="articleBody articleContentWidth">${escapeHTML(current.body).replaceAll("\n", "<br>")}</div>
          </div>
        `;
        return;
      }

      const board = boards[state.boardIndex];
      const page = board ? threadCache[board.room] : null;
      const articles = buildArticles(page ? page.items : []);
      const current = articles[state.threadIndex] || articles[0];
      if (!board) {
        previewMeta.textContent = "尚無可用看板";
        return;
      }
      if (!current) {
        previewMeta.textContent = `${board.name} / 尚無文章`;
        previewBody.innerHTML = `<div class="articleEmpty">目前沒有文章內容。</div>`;
        return;
      }

      const rootMessage = current.messages[0] || null;
      const replies = current.messages.slice(1);
      let bodyHTML = `
        <div class="articleShell">
          <div class="articleHeader">
            <div class="articleHeaderRow">
              <div class="articleHeaderKey">作者</div>
              <div class="articleHeaderValue">${rootMessage ? authorHTML(rootMessage.author) : "-"}</div>
              <div class="articleBoardTag">看板 ${escapeHTML(board.name)}</div>
            </div>
            <div class="articleHeaderRow">
              <div class="articleHeaderKey">標題</div>
              <div class="articleHeaderValue">${escapeHTML(current.title || "(未命名文章)")}</div>
            </div>
            <div class="articleHeaderRow">
              <div class="articleHeaderKey">時間</div>
              <div class="articleHeaderValue">${escapeHTML(rootMessage ? fmtTS(rootMessage.ts) : fmtTS(current.ts))}</div>
            </div>
          </div>
          <div class="articleBody articleContentWidth">${escapeHTML(rootMessage ? rootMessage.body : "").replaceAll("\n", "<br>")}</div>
      `;

      if (replies.length) {
        bodyHTML += `<div class="articleDivider articleContentWidth"></div><div class="replyList articleContentWidth">`;
        bodyHTML += replies.map((message, index) => {
          const mark = replyMark(message.author);
          return `
            <div class="replyRow">
              <div class="${mark.className}">${mark.text}</div>
              <div class="replyNo">${fmtReplyNo(index + 1)}</div>
              <div class="replyAuthor">${authorHTML(message.author)}</div>
              <div class="replyBody">${escapeHTML(message.body).replaceAll("\n", "<br>")}</div>
              <div class="replyMeta">${fmtTS(message.ts)}</div>
            </div>
          `;
        }).join("");
        bodyHTML += `</div>`;
      }

      if (page && page.hasMore) {
        bodyHTML += `<div class="articleDivider articleContentWidth"></div><div class="articleEmpty articleContentWidth">[系統] 尚有更早文章，下一步可再補向前翻頁。</div>`;
      }
      bodyHTML += `</div>`;
      previewBody.innerHTML = bodyHTML;
    }

    function renderChrome() {
      if (state.level === "menu") {
        subBar.innerHTML = `
          <span><span class="hotkey">a)</span>新增看板</span>
          <span><span class="hotkey">b)</span>看板列表</span>
          <span><span class="hotkey">f)</span>訂閱看板</span>
          <span><span class="hotkey">s)</span>搜尋看板</span>
          <span><span class="hotkey">F)</span>全文搜尋</span>
          <span><span class="hotkey">Esc)</span>返回主頁</span>
        `;
        noticeBar.innerHTML = `
          <span>操作：<span class="hotkey">↑↓</span>選取</span>
          <span><span class="hotkey">→</span>進入功能</span>
          <span><span class="hotkey">Enter</span>等同進入</span>
          <span><span class="hotkey">Esc)</span>返回主頁</span>
        `;
        tableHead.className = "tableHead";
        tableHead.textContent = "功能選單";
        breadcrumb.textContent = "主選單 / 看板選單";
        footerText.textContent = `目前選擇：${menuItems[state.menuIndex].label}`;
      } else if (state.level === "boards" || state.level === "search_rooms") {
        subBar.innerHTML = `
          <span><span class="hotkey">↑↓</span>移動選取</span>
          <span><span class="hotkey">→</span>進入看板</span>
          <span><span class="hotkey">←</span>返回主選單</span>
          <span><span class="hotkey">s)</span>搜尋看板</span>
        `;
        noticeBar.innerHTML = `
          <span>操作：<span class="hotkey">↑↓</span>選取看板</span>
          <span><span class="hotkey">→</span>進入文章列表</span>
          <span><span class="hotkey">Enter</span>等同進入</span>
          <span><span class="hotkey">←</span>返回主選單</span>
        `;
        tableHead.className = "tableHead boardHead";
        tableHead.innerHTML = `
          <div></div>
          <div>編號</div>
          <div>未讀</div>
          <div>板名稱</div>
          <div>類別</div>
          <div>中文敘述</div>
          <div>人氣</div>
          <div>板主</div>
        `;
        if (state.level === "search_rooms") {
          breadcrumb.textContent = `主選單 / 搜尋看板 / ${state.searchQuery}`;
          const board = searchState.rooms[state.searchIndex];
          footerText.textContent = board
            ? `搜尋結果：${board.name} / ${board.description || board.room}`
            : "搜尋結果：無符合看板";
        } else {
          breadcrumb.textContent = "主選單 / 看板選單 / 看板列表";
          const board = boards[state.boardIndex];
          footerText.textContent = board
            ? `目前選擇：${board.name} / ${board.description || board.room}`
            : "目前沒有可用看板";
        }
      } else if (state.level === "threads" || state.level === "search_messages") {
        if (state.level === "search_messages") {
          subBar.innerHTML = `
            <span><span class="hotkey">↑↓</span>移動文章</span>
            <span><span class="hotkey">→</span>閱讀文章</span>
            <span><span class="hotkey">←</span>返回搜尋結果</span>
            <span><span class="hotkey">F)</span>全文搜尋</span>
          `;
          noticeBar.innerHTML = `
            <span>操作：<span class="hotkey">↑↓</span>選取文章</span>
            <span><span class="hotkey">→</span>閱讀內容</span>
            <span><span class="hotkey">Enter</span>等同閱讀</span>
            <span><span class="hotkey">←</span>返回搜尋結果</span>
          `;
        } else {
          subBar.innerHTML = `
            <span><span class="hotkey">↑↓</span>移動文章</span>
            <span><span class="hotkey">a)</span>新增文章</span>
            <span><span class="hotkey">→</span>閱讀文章</span>
            <span><span class="hotkey">←</span>返回看板列表</span>
            <span><span class="hotkey">F)</span>全文搜尋</span>
          `;
          noticeBar.innerHTML = `
            <span>操作：<span class="hotkey">↑↓</span>選取文章</span>
            <span><span class="hotkey">a)</span>發表文章</span>
            <span><span class="hotkey">→</span>閱讀內容</span>
            <span><span class="hotkey">Enter</span>等同閱讀</span>
            <span><span class="hotkey">←</span>返回看板列表</span>
          `;
        }
        tableHead.className = "tableHead threadHead";
        tableHead.innerHTML = `
          <div></div>
          <div>樓層</div>
          <div>日期</div>
          <div>作者</div>
          <div>標題</div>
          <div>回文數</div>
        `;
        if (state.level === "search_messages") {
          breadcrumb.textContent = `主選單 / 全文搜尋 / ${state.searchQuery}`;
          const current = searchState.messages[state.searchIndex];
          footerText.textContent = current
            ? `搜尋結果：${current.roomName} / ${current.summary}`
            : "搜尋結果：無符合文章";
        } else {
          const board = boards[state.boardIndex];
          breadcrumb.textContent = board
            ? `主選單 / 看板選單 / ${board.name} / 文章列表`
            : "主選單 / 看板選單 / 文章列表";
          const current = board ? buildArticles((threadCache[board.room]?.items) || [])[state.threadIndex] : null;
          footerText.textContent = current && board
            ? `目前選擇：${board.name} / #${current.floor} / ${current.summary}`
            : `${board ? board.name : "目前看板"} / 尚無文章`;
        }
      } else {
        subBar.innerHTML = `
          <span><span class="hotkey">↑↓</span>捲動內容</span>
          <span><span class="hotkey">→</span>向下翻頁</span>
          <span><span class="hotkey">PgUp/PgDn</span>整頁捲動</span>
          <span><span class="hotkey">←</span>返回文章列表</span>
          <span><span class="hotkey">F)</span>全文搜尋</span>
        `;
        noticeBar.innerHTML = `
          <span>操作：<span class="hotkey">↑↓</span>小幅捲動</span>
          <span><span class="hotkey">Enter</span>回文</span>
          <span><span class="hotkey">r)</span>正式回文</span>
          <span><span class="hotkey">→</span>等同 PageDown</span>
          <span><span class="hotkey">PgUp/PgDn</span>整頁捲動</span>
          <span><span class="hotkey">←</span>返回文章列表</span>
        `;
        tableHead.className = "tableHead";
        tableHead.textContent = "文章內容";
        if (state.level === "search_article") {
          const current = searchState.messages[state.searchIndex];
          breadcrumb.textContent = `主選單 / 全文搜尋 / ${state.searchQuery} / 文章內容`;
          footerText.textContent = current
            ? `閱讀中：${current.roomName} / #${current.floor} / ${current.summary}`
            : "搜尋結果：無符合文章";
        } else {
          const board = boards[state.boardIndex];
          const current = board ? buildArticles((threadCache[board.room]?.items) || [])[state.threadIndex] : null;
          breadcrumb.textContent = board
            ? `主選單 / 看板選單 / ${board.name} / 文章列表 / 文章內容`
            : "主選單 / 看板選單 / 文章內容";
          footerText.textContent = current && board
            ? `閱讀中：${board.name} / #${current.floor} / ${current.summary}`
            : `${board ? board.name : "目前看板"} / 尚無文章`;
        }
      }
    }

    function render() {
      setActiveView();
      renderMenu();
      renderBoards();
      renderThreads();
      renderArticle();
      renderChrome();
      persistState();
    }

    function isArticleLevel() {
      return state.level === "article" || state.level === "search_article";
    }

    function scrollArticleBy(delta) {
      if (!isArticleLevel()) {
        return;
      }
      articleView.scrollBy({
        top: delta,
        left: 0,
        behavior: "auto"
      });
    }

    function pageScrollArticle(direction) {
      if (!isArticleLevel()) {
        return;
      }
      const amount = Math.max(Math.floor(articleView.clientHeight * 0.85), 120);
      scrollArticleBy(amount * direction);
    }

    function currentBoardContext() {
      const board = boards[state.boardIndex];
      if (!board) return null;
      return {
        projectID: board.projectID,
        roomID: board.roomID,
        boardName: board.name || board.roomID
      };
    }

    function currentArticleContext() {
      if (state.level === "search_article") {
        const current = searchState.messages[state.searchIndex];
        if (!current) return null;
        return {
          projectID: current.projectID,
          roomID: current.roomID,
          articleID: current.articleID || current.id || "",
          title: current.title || "(未命名文章)",
          replyToMessageID: current.id || ""
        };
      }

      const board = boards[state.boardIndex];
      if (!board) return null;
      const articles = buildArticles((threadCache[board.room]?.items) || []);
      const current = articles[state.threadIndex];
      if (!current) return null;
      const rootMessage = current.messages[0] || null;
      return {
        projectID: board.projectID,
        roomID: board.roomID,
        articleID: current.articleID || "",
        title: current.title || "(未命名文章)",
        replyToMessageID: rootMessage?.id || ""
      };
    }

    function openReplyDialog() {
      const ctx = currentArticleContext();
      if (!ctx) {
        footerText.textContent = "目前沒有可回覆的文章";
        return;
      }
      dlgReplyError.style.display = "none";
      dlgReplyError.textContent = "";
      replyArticleTitle.value = ctx.title;
      replyBody.value = "";
      dlgReply.showModal();
      setTimeout(() => replyBody.focus(), 0);
    }

    function openArticleDialog() {
      const ctx = currentBoardContext();
      if (!ctx) {
        footerText.textContent = "目前沒有可發文的看板";
        return;
      }
      articleDialogMode = "new";
      dlgArticleError.style.display = "none";
      dlgArticleError.textContent = "";
      articleBoardName.value = ctx.boardName;
      articleTitle.value = "";
      articleTitle.readOnly = false;
      articleBody.value = "";
      btnSubmitArticle.textContent = "送出文章";
      dlgArticle.showModal();
      setTimeout(() => articleTitle.focus(), 0);
    }

    function openFormalReplyDialog() {
      const article = currentArticleContext();
      const board = currentBoardContext();
      if (!article || !board) {
        footerText.textContent = "目前沒有可正式回文的文章";
        return;
      }
      articleDialogMode = "formal-reply";
      dlgArticleError.style.display = "none";
      dlgArticleError.textContent = "";
      articleBoardName.value = board.boardName;
      articleTitle.value = `Re: ${article.title || "(未命名文章)"}`;
      articleTitle.readOnly = true;
      articleBody.value = "";
      btnSubmitArticle.textContent = "送出正式回文";
      dlgArticle.showModal();
      setTimeout(() => articleBody.focus(), 0);
    }

    async function submitReply() {
      const ctx = currentArticleContext();
      if (!ctx) {
        dlgReplyError.textContent = "找不到目前文章。";
        dlgReplyError.style.display = "block";
        return;
      }
      const text = String(replyBody.value || "").trim();
      if (!text) {
        dlgReplyError.textContent = "請輸入回文內容。";
        dlgReplyError.style.display = "block";
        return;
      }
      if (Array.from(text).length > 512) {
        dlgReplyError.textContent = "回文內容不可超過 512 字。";
        dlgReplyError.style.display = "block";
        return;
      }

      btnSubmitReply.disabled = true;
      dlgReplyError.style.display = "none";
      dlgReplyError.textContent = "";
      try {
        await apiPost(`/api/projects/${encodeURIComponent(ctx.projectID)}/rooms/${encodeURIComponent(ctx.roomID)}/messages`, {
          agent_id: getClientID(),
          text,
          article_id: ctx.articleID,
          title: ctx.title,
          reply_to_message_id: ctx.replyToMessageID
        });

        if (state.level === "search_article") {
          await runMessageSearch(state.searchQuery);
        } else {
          const board = boards[state.boardIndex];
          if (board) {
            delete threadCache[board.room];
            await ensureThreadsLoaded();
          }
        }

        dlgReply.close();
        render();
      } catch (error) {
        dlgReplyError.textContent = String(error?.message || error || "送出回文失敗");
        dlgReplyError.style.display = "block";
      } finally {
        btnSubmitReply.disabled = false;
      }
    }

    function makeArticleID() {
      const stamp = Date.now().toString(36);
      const rand = Math.random().toString(36).slice(2, 8);
      return `article-${stamp}-${rand}`;
    }

    async function submitArticle() {
      const ctx = currentBoardContext();
      if (!ctx) {
        dlgArticleError.textContent = "找不到目前看板。";
        dlgArticleError.style.display = "block";
        return;
      }

      const title = String(articleTitle.value || "").trim();
      const text = String(articleBody.value || "").trim();
      if (!title) {
        dlgArticleError.textContent = "請輸入文章標題。";
        dlgArticleError.style.display = "block";
        return;
      }
      if (!text) {
        dlgArticleError.textContent = "請輸入文章內容。";
        dlgArticleError.style.display = "block";
        return;
      }

      btnSubmitArticle.disabled = true;
      dlgArticleError.style.display = "none";
      dlgArticleError.textContent = "";
      try {
        let replyToMessageID = "";
        if (articleDialogMode === "formal-reply") {
          const current = currentArticleContext();
          if (!current) {
            throw new Error("找不到目前文章。");
          }
          replyToMessageID = current.replyToMessageID || "";
        }
        await apiPost(`/api/projects/${encodeURIComponent(ctx.projectID)}/rooms/${encodeURIComponent(ctx.roomID)}/messages`, {
          agent_id: getClientID(),
          text,
          article_id: makeArticleID(),
          title,
          reply_to_message_id: replyToMessageID || undefined
        });

        const board = boards[state.boardIndex];
        if (board) {
          delete threadCache[board.room];
          const page = await ensureThreadsLoaded();
          const articles = buildArticles(page.items || []);
          state.threadIndex = Math.max(articles.length - 1, 0);
        }

        dlgArticle.close();
        render();
      } catch (error) {
        dlgArticleError.textContent = String(error?.message || error || "送出文章失敗");
        dlgArticleError.style.display = "block";
      } finally {
        btnSubmitArticle.disabled = false;
      }
    }

    async function enterNextLevel() {
      if (state.level === "menu") {
        if (state.menuIndex === 0) {
          openBoardDialog();
          return;
        }
        if (state.menuIndex === 3) {
          await promptSearch("rooms");
          return;
        }
        if (state.menuIndex === 4) {
          await promptSearch("messages");
          return;
        }
        state.level = "boards";
      } else if (state.level === "boards") {
        const page = await ensureThreadsLoaded();
        const lastTS = page.items.length ? page.items[page.items.length - 1].ts : "";
        markRoomRead(boards[state.boardIndex]?.room, lastTS);
        updateBoardUnread();
        state.level = "threads";
      } else if (state.level === "threads") {
        state.level = "article";
        articleView.scrollTop = 0;
      } else if (state.level === "search_rooms") {
        const board = searchState.rooms[state.searchIndex];
        if (!board) {
          render();
          return;
        }
        const realIndex = boards.findIndex((item) => item.room === board.room);
        if (realIndex >= 0) {
          state.boardIndex = realIndex;
          state.threadIndex = 0;
          const page = await ensureThreadsLoaded();
          const lastTS = page.items.length ? page.items[page.items.length - 1].ts : "";
          markRoomRead(boards[state.boardIndex]?.room, lastTS);
          updateBoardUnread();
          state.level = "threads";
        }
      } else if (state.level === "search_messages") {
        state.level = "search_article";
        articleView.scrollTop = 0;
      }
      render();
    }

    function backPrevLevel() {
      if (state.level === "search_article") {
        state.level = "search_messages";
      } else if (state.level === "search_messages") {
        state.level = "menu";
      } else if (state.level === "search_rooms") {
        state.level = "menu";
      } else if (state.level === "article") {
        state.level = "threads";
      } else if (state.level === "threads") {
        state.level = "boards";
      } else if (state.level === "boards") {
        state.level = "menu";
      }
      render();
    }

    function move(delta) {
      if (state.level === "menu") {
        state.menuIndex = Math.max(0, Math.min(menuItems.length - 1, state.menuIndex + delta));
      } else if (state.level === "search_rooms") {
        state.searchIndex = Math.max(0, Math.min(searchState.rooms.length - 1, state.searchIndex + delta));
      } else if (state.level === "search_messages") {
        state.searchIndex = Math.max(0, Math.min(searchState.messages.length - 1, state.searchIndex + delta));
      } else if (state.level === "boards") {
        state.boardIndex = Math.max(0, Math.min(boards.length - 1, state.boardIndex + delta));
        state.threadIndex = 0;
      } else if (state.level === "threads" || state.level === "article") {
        const board = boards[state.boardIndex];
        const articles = board ? buildArticles((threadCache[board.room]?.items) || []) : [];
        state.threadIndex = Math.max(0, Math.min(Math.max(articles.length - 1, 0), state.threadIndex + delta));
      } else if (state.level === "search_article") {
        state.searchIndex = Math.max(0, Math.min(searchState.messages.length - 1, state.searchIndex + delta));
      }
      render();
    }

    document.addEventListener("keydown", async (event) => {
      if (dlgBoard.open) {
        if (event.key === "Escape") {
          event.preventDefault();
          dlgBoard.close();
        } else if (event.key === "Enter" && !event.shiftKey) {
          const target = event.target;
          if (target instanceof HTMLInputElement || target instanceof HTMLSelectElement) {
            event.preventDefault();
            await createBoard();
          }
        }
        return;
      }
      if (dlgReply.open) {
        if (event.key === "Escape") {
          event.preventDefault();
          dlgReply.close();
          return;
        }
        if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
          event.preventDefault();
          await submitReply();
          return;
        }
        return;
      }
      if (dlgArticle.open) {
        if (event.key === "Escape") {
          event.preventDefault();
          dlgArticle.close();
          return;
        }
        if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
          event.preventDefault();
          await submitArticle();
          return;
        }
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        if (isArticleLevel()) {
          scrollArticleBy(-48);
          return;
        }
        move(-1);
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (isArticleLevel()) {
          scrollArticleBy(48);
          return;
        }
        move(1);
        return;
      }
      if (event.key === "PageUp") {
        event.preventDefault();
        if (isArticleLevel()) {
          pageScrollArticle(-1);
          return;
        }
        move(-10);
        return;
      }
      if (event.key === "PageDown") {
        event.preventDefault();
        if (isArticleLevel()) {
          pageScrollArticle(1);
          return;
        }
        move(10);
        return;
      }
      if (event.key === "ArrowRight" || event.key === "Enter") {
        event.preventDefault();
        if (isArticleLevel() && event.key === "ArrowRight") {
          pageScrollArticle(1);
          return;
        }
        if (isArticleLevel() && event.key === "Enter") {
          openReplyDialog();
          return;
        }
        await enterNextLevel();
        return;
      }
      if (event.key === "ArrowLeft") {
        event.preventDefault();
        backPrevLevel();
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        if (state.level === "menu") {
          window.location.href = "/main.html";
          return;
        }
        backPrevLevel();
        return;
      }
      if (event.key === "s" || event.key === "S") {
        event.preventDefault();
        await promptSearch("rooms");
        return;
      }
      if ((event.key === "a" || event.key === "A") && state.level === "menu") {
        event.preventDefault();
        state.menuIndex = 0;
        render();
        openBoardDialog();
        return;
      }
      if ((event.key === "a" || event.key === "A") && state.level === "threads") {
        event.preventDefault();
        openArticleDialog();
        return;
      }
      if ((event.key === "r" || event.key === "R") && isArticleLevel()) {
        event.preventDefault();
        openFormalReplyDialog();
        return;
      }
      if (event.key === "f" || event.key === "F") {
        event.preventDefault();
        await promptSearch("messages");
      }
    });

    newBoardProjectSelect.addEventListener("change", onCreateBoardProjectChanged);
    btnCloseBoardDlg.addEventListener("click", () => dlgBoard.close());
    btnCreateBoard.addEventListener("click", async () => {
      await createBoard();
    });
    btnCloseReplyDlg.addEventListener("click", () => dlgReply.close());
    btnSubmitReply.addEventListener("click", async () => {
      await submitReply();
    });
    btnCloseArticleDlg.addEventListener("click", () => dlgArticle.close());
    btnSubmitArticle.addEventListener("click", async () => {
      await submitArticle();
    });

    async function init() {
      tickStatus();
      setInterval(tickStatus, 1000);
      if (!getAuthToken()) {
        clearSessionAndRedirect();
        return;
      }
      setInterval(checkSessionAlive, 60000);
      try {
        await loadBoards();
        const restored = restoreState();
        if (!restored && boards.length > 0) {
          state.boardIndex = 0;
          state.threadIndex = 0;
          state.level = "threads";
        }
        if (state.level === "threads" || state.level === "article") {
          const page = await ensureThreadsLoaded();
          const lastTS = page.items.length ? page.items[page.items.length - 1].ts : "";
          markRoomRead(boards[state.boardIndex]?.room, lastTS);
          updateBoardUnread();
        }
        render();
      } catch (error) {
        console.error(error);
        const message = String(error?.message || "資料載入失敗");
        if (message.includes("unauthorized")) {
          clearSessionAndRedirect();
          return;
        }
        tableHead.className = "tableHead";
        tableHead.textContent = "載入失敗";
        breadcrumb.textContent = "主選單 / 載入失敗";
        subBar.innerHTML = `<span>無法載入看板資料，請確認已登入且 API 可用。</span>`;
        menuView.classList.add("active");
        boardView.classList.remove("active");
        threadView.classList.remove("active");
        articleView.classList.remove("active");
        menuList.innerHTML = `<div class="menuRow activeRow"><div class="menuKey">!</div><div>${escapeHTML(message)}</div></div>`;
        footerText.textContent = "請先確認登入狀態與 API 權限";
      }
    }

    init();
  
