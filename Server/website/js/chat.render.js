    let _lastRenderedLevel = null;
    let _lastBoardsSource = null;
    let _lastBoardsLen = 0;
    let _lastThreadsSource = null;
    let _lastThreadsLen = 0;
    let _lastRenderedArticleKey = "";
    let _lastChromeLevel = null;
    let _lastChromeEditable = null;
    let _lastMenuLen = 0;

    function updateActiveRow(container, newIndex) {
      if (!container) return;
      const rows = container.children;
      if (!rows || !rows.length) return;
      for (let i = 0; i < rows.length; i++) {
        const row = rows[i];
        if (i === newIndex) {
          if (!row.classList.contains("activeRow")) {
            row.classList.add("activeRow");
          }
          row.scrollIntoView({ block: "nearest" });
        } else if (row.classList.contains("activeRow")) {
          row.classList.remove("activeRow");
        }
      }
    }

    function renderMenu() {
      const frag = document.createDocumentFragment();
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
        frag.appendChild(row);
      });
      menuList.replaceChildren(frag);
      _lastMenuLen = menuItems.length;
    }

    function renderBoards() {
      const source = state.level === "search_rooms" ? searchState.rooms : boards;
      updateBoardUnread();
      const frag = document.createDocumentFragment();
      source.forEach((board, index) => {
        const row = document.createElement("div");
        const active = (state.level === "boards" && state.boardIndex === index) || (state.level === "search_rooms" && state.searchIndex === index);
        row.className = "boardRow" + (active ? " activeRow" : "");
        row.innerHTML = `
          <div class="boardCursorCol"></div>
          <div class="boardNo">${fmtReplyNo(index + 1)}</div>
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
        frag.appendChild(row);
      });
      boardList.replaceChildren(frag);
      _lastBoardsSource = source;
      _lastBoardsLen = source.length;
    }

    function renderThreads() {
      const isSearch = state.level === "search_messages";
      const frag = document.createDocumentFragment();

      if (isSearch) {
        const items = searchState.messages;
        items.forEach((thread, index) => {
          const row = document.createElement("div");
          row.className = "threadRow" + (state.searchIndex === index ? " activeRow" : "");
          row.innerHTML = `
            <div class="boardCursorCol"></div>
            <div class="threadFloor">${fmtReplyNo(thread.floor)}</div>
            <div class="threadDate">${escapeHTML(fmtDay(thread.ts))}</div>
            <div class="threadAuthor">${authorHTML(thread.author)}</div>
            <div class="threadTitle"><span class="articleMark">■</span><span class="threadTitleText">${escapeHTML(thread.title || `${thread.roomName} · ${thread.summary}`)}</span></div>
            <div class="threadTodayReplies">-</div>
            <div class="threadReplies">-</div>
          `;
          row.addEventListener("click", async () => {
            state.searchIndex = index;
            await enterNextLevel();
          });
          frag.appendChild(row);
        });
        threadList.replaceChildren(frag);
        _lastThreadsSource = items;
        _lastThreadsLen = items.length;
        return;
      }

      const board = boards[state.boardIndex];
      const page = board ? threadCache[board.room] : null;
      const articles = buildArticles(page ? page.items : []);
      if (!board) {
        threadList.replaceChildren();
        _lastThreadsSource = null;
        _lastThreadsLen = 0;
        return;
      }

      articles.forEach((thread, index) => {
        const row = document.createElement("div");
        row.className = "threadRow" + (state.level === "threads" && state.threadIndex === index ? " activeRow" : "");
        row.innerHTML = `
          <div class="boardCursorCol"></div>
          <div class="threadFloor">${fmtReplyNo(thread.floor)}</div>
          <div class="threadDate">${escapeHTML(fmtDay(thread.ts))}</div>
          <div class="threadAuthor">${authorHTML(thread.author)}</div>
          <div class="threadTitle"><span class="articleMark">■</span><span class="threadTitleText">${escapeHTML(thread.title || "(未命名文章)")}</span></div>
          <div class="threadTodayReplies ${thread.todayReplyCount > 0 ? "todayActive" : ""}">${thread.todayReplyCount}</div>
          <div class="threadReplies">${thread.replyCount}</div>
        `;
        row.addEventListener("click", async () => {
          state.threadIndex = index;
          await enterNextLevel();
        });
        frag.appendChild(row);
      });
      threadList.replaceChildren(frag);
      _lastThreadsSource = page ? page.items : null;
      _lastThreadsLen = page && page.items ? page.items.length : 0;
    }

    const IMAGE_EXT_REGEX = /\.(?:png|jpe?g|gif|webp|svg|bmp)(?:\?[^\s<>"']*)?$/i;

    function isImageURL(url) {
      if (!url || typeof url !== "string") return false;
      const clean = url.trim().split("#")[0];
      return IMAGE_EXT_REGEX.test(clean);
    }

    function renderImageTag(rawURL, altText = "article image") {
      const url = escapeHTML(rawURL.trim());
      const alt = escapeHTML((altText || "article image").trim());
      return `<div class="articleImageWrap"><a href="${url}" target="_blank" rel="noopener noreferrer"><img class="articleImage" src="${url}" alt="${alt}" loading="lazy" decoding="async" referrerpolicy="no-referrer" onerror="this.style.display='none'; if(this.parentElement && this.parentElement.nextElementSibling) this.parentElement.nextElementSibling.style.display='block';"></a><a class="articleImageFallback" href="${url}" target="_blank" rel="noopener noreferrer" style="display:none;">🖼️ 圖片載入失敗，點此開啟：${url}</a></div>`;
    }

    function renderLinkTag(rawURL, linkText) {
      const url = escapeHTML(rawURL.trim());
      const label = escapeHTML((linkText || rawURL).trim());
      return `<a class="articleLink" href="${url}" target="_blank" rel="noopener noreferrer">${label}</a>`;
    }

    function renderRichArticleBody(text) {
      const source = String(text || "");
      if (!source) return "";

      const tokenRegex = /\[img\]\s*(https?:\/\/[^\s]+?)\s*\[\/img\]|!\[([^\]]*)\]\((https?:\/\/[^\s\)]+)\)|\[([^\]]+)\]\((https?:\/\/[^\s\)]+)\)|(https?:\/\/[^\s<>"'()[\]{}]+)/gi;

      let cursor = 0;
      let html = "";
      let match;

      while ((match = tokenRegex.exec(source)) !== null) {
        const before = source.slice(cursor, match.index);
        if (before) {
          html += escapeHTML(before).replaceAll("\n", "<br>");
        }

        if (match[1]) {
          // [img]url[/img]
          html += renderImageTag(match[1]);
        } else if (match[3]) {
          // ![alt](url)
          html += renderImageTag(match[3], match[2]);
        } else if (match[4] && match[5]) {
          // [text](url)
          const linkText = match[4];
          const linkURL = match[5];
          if (isImageURL(linkURL)) {
            html += renderLinkTag(linkURL, linkText) + "<br>" + renderImageTag(linkURL, linkText);
          } else {
            html += renderLinkTag(linkURL, linkText);
          }
        } else if (match[6]) {
          // Plain URL (https://...)
          const rawURL = match[6];
          if (isImageURL(rawURL)) {
            html += renderLinkTag(rawURL, rawURL) + "<br>" + renderImageTag(rawURL);
          } else {
            html += renderLinkTag(rawURL, rawURL);
          }
        }

        cursor = match.index + match[0].length;
      }

      const tail = source.slice(cursor);
      if (tail) {
        html += escapeHTML(tail).replaceAll("\n", "<br>");
      }

      return html;
    }

    function renderArticle() {
      if (state.level === "search_article") {
        const current = searchState.messages[state.searchIndex];
        const key = current ? `search_${state.searchIndex}_${current.id || ""}` : "search_empty";
        if (_lastRenderedArticleKey === key) return;
        _lastRenderedArticleKey = key;

        previewMeta.textContent = "";
        if (!current) {
          previewMeta.textContent = "全文搜尋 / 無結果";
          previewBody.innerHTML = "";
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
            <div class="articleBody articleContentWidth">${renderRichArticleBody(current.body)}</div>
          </div>
        `;
        return;
      }

      const board = boards[state.boardIndex];
      const page = board ? threadCache[board.room] : null;
      const articles = buildArticles(page ? page.items : []);
      const current = articles[state.threadIndex] || articles[0];
      const articleKey = board && current ? `${board.room}_${current.articleID}_${current.messages.length}` : (board ? `${board.room}_empty` : "none");
      if (_lastRenderedArticleKey === articleKey) return;
      _lastRenderedArticleKey = articleKey;

      previewMeta.textContent = "";
      if (!board) {
        previewMeta.textContent = "尚無可用看板";
        previewBody.innerHTML = "";
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
          <div class="articleBody articleContentWidth">${renderRichArticleBody(rootMessage ? rootMessage.body : "")}</div>
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
              <div class="replyBody">${renderRichArticleBody(message.body)}</div>
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
      const editable = state.level === "article" && canEditCurrentArticle();
      const levelChanged = _lastChromeLevel !== state.level || _lastChromeEditable !== editable;
      _lastChromeLevel = state.level;
      _lastChromeEditable = editable;

      if (state.level === "menu") {
        if (levelChanged) {
          subBar.innerHTML = `
            <span data-action="goto-boards"><span class="hotkey">b)</span>看板列表</span>
            <span data-action="goto-boards"><span class="hotkey">f)</span>訂閱看板</span>
            <span data-action="search-rooms"><span class="hotkey">s)</span>搜尋看板</span>
            <span data-action="search-messages"><span class="hotkey">F)</span>全文搜尋</span>
            <span data-action="goto-menu"><span class="hotkey">Esc)</span>返回主頁</span>
          `;
          noticeBar.innerHTML = `
            <span>操作：<span class="hotkey">↑↓</span>選取</span>
            <span data-action="next"><span class="hotkey">→</span>進入功能</span>
            <span data-action="next"><span class="hotkey">Enter</span>等同進入</span>
            <span data-action="goto-menu"><span class="hotkey">Esc)</span>返回主頁</span>
          `;
          tableHead.className = "tableHead";
          tableHead.textContent = "功能選單";
          breadcrumb.textContent = "主選單｜看板選單";
        }
      } else if (state.level === "boards" || state.level === "search_rooms") {
        if (levelChanged) {
          subBar.innerHTML = `
            <span><span class="hotkey">↑↓</span>移動選取</span>
            ${isRootUser() ? '<span data-action="new-board"><span class="hotkey">a)</span>新增看板</span>' : ''}
            ${isRootUser() ? '<span data-action="edit-board"><span class="hotkey">e)</span>編輯看板</span>' : ''}
            <span data-action="next"><span class="hotkey">→</span>進入看板</span>
            <span data-action="prev"><span class="hotkey">←</span>返回主選單</span>
            <span data-action="search-rooms"><span class="hotkey">s)</span>搜尋看板</span>
          `;
          noticeBar.innerHTML = `
            <span>操作：<span class="hotkey">↑↓</span>選取看板</span>
            ${isRootUser() ? '<span data-action="new-board"><span class="hotkey">a)</span>新增看板</span>' : ''}
            ${isRootUser() ? '<span data-action="edit-board"><span class="hotkey">e)</span>編輯看板</span>' : ''}
            <span data-action="next"><span class="hotkey">→</span>進入文章列表</span>
            <span data-action="next"><span class="hotkey">Enter</span>等同進入</span>
            <span data-action="prev"><span class="hotkey">←</span>返回主選單</span>
          `;
          tableHead.className = "tableHead boardHead";
          tableHead.innerHTML = `
            <div></div>
            <div>編號</div>
            <div>看板名稱</div>
            <div>類別</div>
            <div>內容敘述</div>
            <div>人氣</div>
            <div>板主</div>
          `;
        }
        if (state.level === "search_rooms") {
          breadcrumb.textContent = `主選單｜搜尋看板｜${state.searchQuery}`;
          const board = searchState.rooms[state.searchIndex];
        } else {
          breadcrumb.textContent = "主選單｜看板選單｜看板列表";
          const board = boards[state.boardIndex];
        }
      } else if (state.level === "threads" || state.level === "search_messages") {
        if (levelChanged) {
          if (state.level === "search_messages") {
            subBar.innerHTML = `
              <span><span class="hotkey">↑↓</span>移動文章</span>
              <span data-action="next"><span class="hotkey">→</span>閱讀文章</span>
              <span data-action="prev"><span class="hotkey">←</span>返回搜尋結果</span>
              <span data-action="search-messages"><span class="hotkey">F)</span>全文搜尋</span>
            `;
            noticeBar.innerHTML = `
              <span>操作：<span class="hotkey">↑↓</span>選取文章</span>
              <span data-action="next"><span class="hotkey">→</span>閱讀內容</span>
              <span data-action="next"><span class="hotkey">Enter</span>等同閱讀</span>
              <span data-action="prev"><span class="hotkey">←</span>返回搜尋結果</span>
            `;
          } else {
            subBar.innerHTML = `
              <span><span class="hotkey">↑↓</span>移動文章</span>
              <span data-action="new-article"><span class="hotkey">a)</span>新增文章</span>
              <span data-action="next"><span class="hotkey">→</span>閱讀文章</span>
              <span data-action="prev"><span class="hotkey">←</span>返回看板列表</span>
              <span data-action="search-messages"><span class="hotkey">F)</span>全文搜尋</span>
            `;
            noticeBar.innerHTML = `
              <span>操作：<span class="hotkey">↑↓</span>選取文章</span>
              <span data-action="new-article"><span class="hotkey">a)</span>發表文章</span>
              <span data-action="next"><span class="hotkey">→</span>閱讀內容</span>
              <span data-action="next"><span class="hotkey">Enter</span>等同閱讀</span>
              <span data-action="prev"><span class="hotkey">←</span>返回看板列表</span>
            `;
          }
          tableHead.className = "tableHead threadHead";
          tableHead.innerHTML = `
            <div></div>
            <div>編號</div>
            <div>日期</div>
            <div>作者</div>
            <div>標題</div>
            <div>今日回文</div>
            <div>回文數</div>
          `;
        }
        if (state.level === "search_messages") {
          breadcrumb.textContent = `主選單｜全文搜尋｜${state.searchQuery}`;
          const current = searchState.messages[state.searchIndex];
        } else {
          const board = boards[state.boardIndex];
          breadcrumb.textContent = board
            ? `主選單｜看板選單｜${board.name}｜文章列表`
            : "主選單｜看板選單｜文章列表";
          const current = board ? buildArticles((threadCache[board.room]?.items) || [])[state.threadIndex] : null;
        }
      } else {
        if (levelChanged) {
          subBar.innerHTML = `
            <span><span class="hotkey">↑↓</span>捲動內容</span>
            <span data-action="scroll-down"><span class="hotkey">→</span>向下翻頁</span>
            <span data-action="scroll-down"><span class="hotkey">PgUp/PgDn</span>整頁捲動</span>
            ${editable ? '<span data-action="edit-article"><span class="hotkey">e)</span>編輯文章</span>' : ''}
            <span data-action="prev"><span class="hotkey">←</span>返回文章列表</span>
            <span data-action="search-messages"><span class="hotkey">F)</span>全文搜尋</span>
          `;
          noticeBar.innerHTML = `
            <span>操作：<span class="hotkey">↑↓</span>小幅捲動</span>
            <span data-action="reply"><span class="hotkey">Enter</span>回文</span>
            <span data-action="formal-reply"><span class="hotkey">r)</span>正式回文</span>
            ${editable ? '<span data-action="edit-article"><span class="hotkey">e)</span>編輯文章</span>' : ''}
            <span data-action="scroll-down"><span class="hotkey">→</span>等同 PageDown</span>
            <span data-action="scroll-down"><span class="hotkey">PgUp/PgDn</span>整頁捲動</span>
            <span data-action="prev"><span class="hotkey">←</span>返回文章列表</span>
          `;
          tableHead.className = "tableHead";
          tableHead.textContent = "文章內容";
        }
        if (state.level === "search_article") {
          const current = searchState.messages[state.searchIndex];
          breadcrumb.textContent = `主選單｜全文搜尋｜${state.searchQuery}｜文章內容`;
        } else {
          const board = boards[state.boardIndex];
          const current = board ? buildArticles((threadCache[board.room]?.items) || [])[state.threadIndex] : null;
          breadcrumb.textContent = board
            ? `主選單｜看板選單｜${board.name}｜文章列表｜文章內容`
            : "主選單｜看板選單｜文章內容";
        }
      }
    }

    function render(force = false) {
      const level = state.level;
      const levelChanged = _lastRenderedLevel !== level;
      _lastRenderedLevel = level;

      if (levelChanged) {
        setActiveView();
      }

      if (level === "menu") {
        if (force || levelChanged || _lastMenuLen !== menuItems.length) {
          renderMenu();
        } else {
          updateActiveRow(menuList, state.menuIndex);
        }
      } else if (level === "boards" || level === "search_rooms") {
        const source = level === "search_rooms" ? searchState.rooms : boards;
        const activeIdx = level === "search_rooms" ? state.searchIndex : state.boardIndex;
        if (force || levelChanged || _lastBoardsSource !== source || _lastBoardsLen !== source.length) {
          renderBoards();
        } else {
          updateActiveRow(boardList, activeIdx);
        }
      } else if (level === "threads" || level === "search_messages") {
        const isSearch = level === "search_messages";
        const board = boards[state.boardIndex];
        const page = board ? threadCache[board.room] : null;
        const items = isSearch ? searchState.messages : (page ? page.items : null);
        const activeIdx = isSearch ? state.searchIndex : state.threadIndex;
        if (force || levelChanged || _lastThreadsSource !== items || _lastThreadsLen !== (items ? items.length : 0)) {
          renderThreads();
        } else {
          updateActiveRow(threadList, activeIdx);
        }
      } else if (level === "article" || level === "search_article") {
        renderArticle();
      }

      renderChrome();
      persistState();
    }
