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
      const rows = container.querySelectorAll(".menuRow, .boardRow, .threadRow");
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

    const PINNED_BOARD_ROOMS = new Set(["announce", "apply", "board-apply", "feedback", "lobby", "visitors"]);

    function isPinnedBoard(board) {
      if (!board) return false;
      const room = String(board.room || board.room_id || board.board || "").toLowerCase().trim();
      return PINNED_BOARD_ROOMS.has(room);
    }

    function renderBoards() {
      const source = state.level === "search_rooms" ? searchState.rooms : boards;
      updateBoardUnread();
      const frag = document.createDocumentFragment();
      const isNormalBoardList = state.level === "boards";

      source.forEach((board, index) => {
        const row = document.createElement("div");
        const isPinned = isNormalBoardList && isPinnedBoard(board);
        const active = (state.level === "boards" && state.boardIndex === index) || (state.level === "search_rooms" && state.searchIndex === index);
        row.className = "boardRow" + (isPinned ? " pinnedRow" : "") + (active ? " activeRow" : "");
        row.innerHTML = `
          <div class="boardCursorCol"></div>
          <div class="boardNo">${fmtReplyNo(index + 1)}</div>
          <div class="boardName" title="${escapeHTML(board.name)}">${escapeHTML(board.name)}</div>
          <div>${escapeHTML(board.category)}</div>
          <div class="boardDesc">${escapeHTML(board.description || board.room)}</div>
          <div class="boardHot">${board.hot}</div>
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

        // 如果是置頂看板的最後一個，且後續還有一般看板，插入符合 BBS 風格的分隔線
        if (isNormalBoardList && isPinnedBoard(board) && (index + 1 < source.length && !isPinnedBoard(source[index + 1]))) {
          const divider = document.createElement("div");
          divider.className = "boardPinnedDivider";
          frag.appendChild(divider);
        }
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
            <div class="threadAuthor" title="${escapeHTML(authorLabel(thread.author))}">${authorHTML(thread.author, 15)}</div>
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
          <div class="threadAuthor" title="${escapeHTML(authorLabel(thread.author))}">${authorHTML(thread.author, 15)}</div>
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
      const label = (linkText || rawURL).trim();
      return `<a class="articleLink" href="${url}" target="_blank" rel="noopener noreferrer">${label}</a>`;
    }

    function renderMath(mathCode, displayMode = false) {
      const code = String(mathCode || "").trim();
      if (!code) return "";
      if (typeof window !== "undefined" && window.katex && typeof window.katex.renderToString === "function") {
        try {
          return window.katex.renderToString(code, {
            displayMode,
            throwOnError: false
          });
        } catch (_) {}
      }
      const safe = escapeHTML(code);
      return displayMode ? `<div class="mathBlock">${safe}</div>` : `<span class="mathInline">${safe}</span>`;
    }

    function parseInlineFormatting(str) {
      return str
        .replace(/\*\*(.+?)\*\*/g, '<strong class="mdBold">$1</strong>')
        .replace(/__(.+?)__/g, '<strong class="mdBold">$1</strong>')
        .replace(/~~(.+?)~~/g, '<del class="mdDel">$1</del>')
        .replace(/\*([^*\n]+)\*/g, '<em class="mdItalic">$1</em>')
        .replace(/(^|\s)_([^_\n]+)_(?=\s|$)/g, '$1<em class="mdItalic">$2</em>');
    }

    function parseInlineMarkdown(text) {
      if (!text) return "";

      // 1. Inline code: `code`
      const codePlaceholders = [];
      let str = text.replace(/`([^`]+)`/g, (_, code) => {
        const idx = codePlaceholders.length;
        codePlaceholders.push(`<code class="inlineCode">${code}</code>`);
        return `\x1A_CODE_${idx}_\x1A`;
      });

      // 2. LaTeX Math:
      const mathPlaceholders = [];
      str = str.replace(/\$\$([\s\S]+?)\$\$/g, (_, math) => {
        const idx = mathPlaceholders.length;
        mathPlaceholders.push(renderMath(math, true));
        return `\x1A_MATH_${idx}_\x1A`;
      });
      str = str.replace(/\\\[([\s\S]+?)\\\]/g, (_, math) => {
        const idx = mathPlaceholders.length;
        mathPlaceholders.push(renderMath(math, true));
        return `\x1A_MATH_${idx}_\x1A`;
      });
      str = str.replace(/\\\(([\s\S]+?)\\\)/g, (_, math) => {
        const idx = mathPlaceholders.length;
        mathPlaceholders.push(renderMath(math, false));
        return `\x1A_MATH_${idx}_\x1A`;
      });
      str = str.replace(/\$([^\$\n\r]+?)\$/g, (_, math) => {
        const idx = mathPlaceholders.length;
        mathPlaceholders.push(renderMath(math, false));
        return `\x1A_MATH_${idx}_\x1A`;
      });

      // 3. Images, Links, and URLs
      const tokenRegex = /\[img\]\s*(https?:\/\/[^\s]+?)\s*\[\/img\]|!\[([^\]]*)\]\((https?:\/\/[^\s\)]+)\)|\[([^\]]+)\]\((https?:\/\/[^\s\)]+)\)|(https?:\/\/[^\s<>"'()[\]{}]+)/gi;

      str = str.replace(tokenRegex, (match, bbImg, mdImgAlt, mdImgUrl, mdLinkText, mdLinkUrl, plainUrl) => {
        if (bbImg) {
          return renderImageTag(bbImg);
        }
        if (mdImgUrl) {
          return renderImageTag(mdImgUrl, mdImgAlt);
        }
        if (mdLinkText && mdLinkUrl) {
          const parsedText = parseInlineFormatting(mdLinkText);
          if (isImageURL(mdLinkUrl)) {
            return renderLinkTag(mdLinkUrl, parsedText) + "<br>" + renderImageTag(mdLinkUrl, mdLinkText);
          }
          return renderLinkTag(mdLinkUrl, parsedText);
        }
        if (plainUrl) {
          if (isImageURL(plainUrl)) {
            return renderLinkTag(plainUrl, plainUrl) + "<br>" + renderImageTag(plainUrl);
          }
          return renderLinkTag(plainUrl, plainUrl);
        }
        return match;
      });

      // 4. Bold, Italic, Strikethrough
      str = parseInlineFormatting(str);

      // 5. Restore LaTeX Math
      str = str.replace(/\x1A_MATH_(\d+)_\x1A/g, (_, idx) => mathPlaceholders[Number(idx)] || "");

      // 6. Restore inline code
      str = str.replace(/\x1A_CODE_(\d+)_\x1A/g, (_, idx) => codePlaceholders[Number(idx)] || "");

      return str;
    }

    function renderRichArticleBody(text) {
      const raw = String(text || "");
      if (!raw) return "";

      const lines = raw.split(/\r?\n/);
      const out = [];
      let inCodeBlock = false;
      let codeBlockLang = "";
      let codeBlockLines = [];
      let inMathBlock = false;
      let mathBlockLines = [];
      let inList = false;
      let listType = "";
      let inBlockquote = false;
      let blockquoteLines = [];
      let inTable = false;
      let tableRows = [];

      function flushCodeBlock() {
        if (!inCodeBlock) return;
        const codeContent = escapeHTML(codeBlockLines.join("\n"));
        const langClass = codeBlockLang ? ` language-${escapeHTML(codeBlockLang)}` : "";
        out.push(`<pre class="codeBlock${langClass}"><code>${codeContent}</code></pre>`);
        inCodeBlock = false;
        codeBlockLang = "";
        codeBlockLines = [];
      }

      function flushMathBlock() {
        if (!inMathBlock) return;
        const mathContent = mathBlockLines.join("\n");
        out.push(`<div class="mathBlockWrap">${renderMath(mathContent, true)}</div>`);
        inMathBlock = false;
        mathBlockLines = [];
      }

      function flushList() {
        if (!inList) return;
        out.push(listType === "ol" ? "</ol>" : "</ul>");
        inList = false;
        listType = "";
      }

      function flushBlockquote() {
        if (!inBlockquote) return;
        const content = blockquoteLines.map(l => parseInlineMarkdown(escapeHTML(l))).join("<br>");
        out.push(`<blockquote class="mdBlockquote">${content}</blockquote>`);
        inBlockquote = false;
        blockquoteLines = [];
      }

      function flushTable() {
        if (!inTable) return;
        if (tableRows.length >= 2) {
          const headerCells = tableRows[0];
          const dataRows = tableRows.slice(2);
          let tableHTML = '<div class="mdTableWrap"><table class="mdTable"><thead><tr>';
          for (const cell of headerCells) {
            tableHTML += `<th>${parseInlineMarkdown(escapeHTML(cell.trim()))}</th>`;
          }
          tableHTML += '</tr></thead><tbody>';
          for (const row of dataRows) {
            tableHTML += '<tr>';
            for (let i = 0; i < headerCells.length; i++) {
              const cell = row[i] !== undefined ? row[i].trim() : '';
              tableHTML += `<td>${parseInlineMarkdown(escapeHTML(cell))}</td>`;
            }
            tableHTML += '</tr>';
          }
          tableHTML += '</tbody></table></div>';
          out.push(tableHTML);
        } else if (tableRows.length === 1) {
          out.push(`<div>${parseInlineMarkdown(escapeHTML(tableRows[0].join(' | ')))}</div>`);
        }
        inTable = false;
        tableRows = [];
      }

      function flushAll() {
        flushList();
        flushBlockquote();
        flushTable();
        flushMathBlock();
      }

      for (let i = 0; i < lines.length; i++) {
        const line = lines[i];

        // 1. Code block fence (```)
        const fenceMatch = line.match(/^```(\w*)/);
        if (fenceMatch) {
          if (inCodeBlock) {
            flushCodeBlock();
          } else {
            flushAll();
            inCodeBlock = true;
            codeBlockLang = fenceMatch[1] || "";
            codeBlockLines = [];
          }
          continue;
        }

        if (inCodeBlock) {
          codeBlockLines.push(line);
          continue;
        }

        const trimmed = line.trim();

        // 2. Math block fence ($$)
        if (trimmed === "$$") {
          if (inMathBlock) {
            flushMathBlock();
          } else {
            flushAll();
            inMathBlock = true;
            mathBlockLines = [];
          }
          continue;
        }

        if (inMathBlock) {
          mathBlockLines.push(line);
          continue;
        }

        // 3. Horizontal Rule (---, ***, ___)
        if (/^(?:-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
          flushAll();
          out.push('<hr class="mdHr">');
          continue;
        }

        // 4. Headings (#, ##, ###, ####, #####, ######)
        const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
        if (headingMatch) {
          flushAll();
          const level = headingMatch[1].length;
          const text = parseInlineMarkdown(escapeHTML(headingMatch[2].trim()));
          out.push(`<h${level} class="mdHeading mdH${level}">${text}</h${level}>`);
          continue;
        }

        // 5. Blockquote (> ...)
        const bqMatch = line.match(/^>\s?(.*)$/);
        if (bqMatch) {
          flushList();
          flushTable();
          inBlockquote = true;
          blockquoteLines.push(bqMatch[1]);
          continue;
        } else if (inBlockquote) {
          flushBlockquote();
        }

        // 6. Table rows (| col1 | col2 |)
        if (/^\|(.+)\|$/.test(trimmed)) {
          flushList();
          flushBlockquote();
          inTable = true;
          const cells = trimmed.slice(1, -1).split('|');
          tableRows.push(cells);
          continue;
        } else if (inTable) {
          flushTable();
        }

        // 7. Unordered Lists (- item, * item, + item)
        const ulMatch = line.match(/^\s*[-*+]\s+(.+)$/);
        if (ulMatch) {
          flushBlockquote();
          flushTable();
          if (!inList || listType !== "ul") {
            flushList();
            inList = true;
            listType = "ul";
            out.push('<ul class="mdList">');
          }
          let itemText = ulMatch[1];
          if (itemText.startsWith("[ ] ")) {
            itemText = '<input type="checkbox" disabled class="mdTask"> ' + itemText.slice(4);
          } else if (itemText.startsWith("[x] ") || itemText.startsWith("[X] ")) {
            itemText = '<input type="checkbox" checked disabled class="mdTask"> ' + itemText.slice(4);
          }
          out.push(`<li>${parseInlineMarkdown(escapeHTML(itemText))}</li>`);
          continue;
        }

        // 8. Ordered Lists (1. item, 2. item)
        const olMatch = line.match(/^\s*(\d+)\.\s+(.+)$/);
        if (olMatch) {
          flushBlockquote();
          flushTable();
          if (!inList || listType !== "ol") {
            flushList();
            inList = true;
            listType = "ol";
            out.push('<ol class="mdList">');
          }
          out.push(`<li>${parseInlineMarkdown(escapeHTML(olMatch[2]))}</li>`);
          continue;
        }

        flushList();

        // 9. Empty lines
        if (!trimmed) {
          out.push('<div class="mdEmptyLine"></div>');
          continue;
        }

        // 10. Regular text lines
        out.push(`<div class="mdLine">${parseInlineMarkdown(escapeHTML(line))}</div>`);
      }

      flushCodeBlock();
      flushMathBlock();
      flushAll();

      return out.join("");
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
      const articleSig = current ? (current.messages || []).map(m => `${m.id}:${m.ts}:${m.title}:${m.body}`).join("|") : "";
      const articleKey = board && current ? `${board.room}_${current.articleID}_${articleSig}` : (board ? `${board.room}_empty` : "none");
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
              <div class="replyMeta">${escapeHTML(fmtTS(message.ts))}</div>
            </div>
          `;
        }).join("");
        bodyHTML += `</div>`;
      }

      if (page && page.hasMore) {
        bodyHTML += `<div class="articleDivider articleContentWidth"></div><div class="articleEmpty articleContentWidth">[系統] 尚有更早文章，下一步可再補向前翻頁。</div>`;
      }
      bodyHTML += `</div>`;

      const prevScroll = articleView ? articleView.scrollTop : 0;
      previewBody.innerHTML = bodyHTML;
      if (prevScroll > 0 && articleView) {
        articleView.scrollTop = prevScroll;
      }
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
          noticeBar.innerHTML = "";
          tableHead.className = "tableHead";
          tableHead.textContent = "功能選單";
          breadcrumb.textContent = "主選單｜看板選單";
        }
      } else if (state.level === "boards" || state.level === "search_rooms") {
        if (levelChanged) {
          subBar.innerHTML = `
            <span><span class="hotkey">↑↓</span>移動選取</span>
            <span data-action="next"><span class="hotkey">→</span>進入看板</span>
            <span data-action="prev"><span class="hotkey">←</span>返回主選單</span>
            <span data-action="search-rooms"><span class="hotkey">s)</span>搜尋看板</span>
          `;
          noticeBar.innerHTML = "";
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
          } else {
            subBar.innerHTML = `
              <span><span class="hotkey">↑↓</span>移動文章</span>
              <span data-action="new-article"><span class="hotkey">a)</span>新增文章</span>
              <span data-action="next"><span class="hotkey">→</span>閱讀文章</span>
              <span data-action="prev"><span class="hotkey">←</span>返回看板列表</span>
              <span data-action="search-messages"><span class="hotkey">F)</span>全文搜尋</span>
            `;
          }
          noticeBar.innerHTML = "";
          tableHead.className = "tableHead threadHead";
          tableHead.innerHTML = `
            <div></div>
            <div>編號</div>
            <div>日期</div>
            <div>作者</div>
            <div>標題</div>
            <div>今日</div>
            <div>人氣</div>
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
          noticeBar.innerHTML = "";
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
