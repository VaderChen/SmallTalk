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
      dlgArticleTitle.textContent = "發表文章";
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
      dlgArticleTitle.textContent = "發表正式回文";
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

    function openEditArticleDialog() {
      const article = currentArticleDetail();
      const board = currentBoardContext();
      if (!article || !board) {
        footerText.textContent = "目前沒有可編輯的文章";
        return;
      }
      if (!canEditCurrentArticle()) {
        footerText.textContent = "只有作者可在 12 小時內編輯文章";
        return;
      }
      const rootMessage = article.messages[0] || null;
      if (!rootMessage) {
        footerText.textContent = "找不到文章內容";
        return;
      }
      articleDialogMode = "edit";
      dlgArticleTitle.textContent = "編輯文章";
      dlgArticleError.style.display = "none";
      dlgArticleError.textContent = "";
      articleBoardName.value = board.boardName;
      articleTitle.value = article.title || "(未命名文章)";
      articleTitle.readOnly = false;
      articleBody.value = rootMessage.body || "";
      btnSubmitArticle.textContent = "儲存變更";
      dlgArticle.showModal();
      setTimeout(() => articleTitle.focus(), 0);
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
        await apiPost(`/api/boards/${encodeURIComponent(ctx.roomID)}/messages`, {
          agent_id: getClientID(),
          text,
          article: ctx.articleID,
          title: ctx.title,
          reply_to_message: ctx.replyToMessageID
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
        refreshStats();
        render(true);
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
        if (articleDialogMode === "edit") {
          const article = currentArticleDetail();
          const rootMessage = article?.messages?.[0];
          if (!article || !rootMessage) {
            throw new Error("找不到目前文章。");
          }
          await apiPost(`/api/boards/${encodeURIComponent(ctx.roomID)}/messages/${encodeURIComponent(rootMessage.id)}`, {
            title,
            text
          });
        } else {
          if (articleDialogMode === "formal-reply") {
            const current = currentArticleContext();
            if (!current) {
              throw new Error("找不到目前文章。");
            }
            replyToMessageID = current.replyToMessageID || "";
          }
          await apiPost(`/api/boards/${encodeURIComponent(ctx.roomID)}/messages`, {
            agent_id: getClientID(),
            text,
            article: makeArticleID(),
            title,
            reply_to_message: replyToMessageID || undefined
          });
        }

        const board = boards[state.boardIndex];
        if (board) {
          delete threadCache[board.room];
          const page = await ensureThreadsLoaded();
          const articles = buildArticles(page.items || []);
          if (articleDialogMode !== "edit") {
            state.threadIndex = Math.max(articles.length - 1, 0);
          }
        }

        dlgArticle.close();
        refreshStats();
        render(true);
      } catch (error) {
        dlgArticleError.textContent = String(error?.message || error || "送出文章失敗");
        dlgArticleError.style.display = "block";
      } finally {
        btnSubmitArticle.disabled = false;
      }
    }

    async function enterNextLevel() {
      if (state.level === "menu") {
        if (state.menuIndex === 2) {
          await promptSearch("rooms");
          return;
        }
        if (state.menuIndex === 3) {
          await promptSearch("messages");
          return;
        }
        state.level = "boards";
      } else if (state.level === "boards") {
        const page = await ensureThreadsLoaded(true);
        const lastTS = page.items.length ? page.items[page.items.length - 1].ts : "";
        markRoomRead(boards[state.boardIndex]?.room, lastTS);
        updateBoardUnread();
        state.level = "threads";
      } else if (state.level === "threads") {
        await ensureThreadsLoaded(true);
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
          const page = await ensureThreadsLoaded(true);
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
      if ((event.key === "e" || event.key === "E") && isArticleLevel()) {
        event.preventDefault();
        openEditArticleDialog();
        return;
      }
      if (event.key === "f" || event.key === "F") {
        event.preventDefault();
        await promptSearch("messages");
      }
    });

    newBoardProjectSelect.addEventListener("change", onCreateBoardProjectChanged);
    btnCloseBoardDlg.addEventListener("click", () => dlgBoard.close());
    btnDeleteBoard.addEventListener("click", async () => {
      await deleteBoard();
    });
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

    async function handleUIAction(action) {
      if (!action) return;
      switch (action) {
        case "next":
          await enterNextLevel();
          break;
        case "prev":
          backPrevLevel();
          break;
        case "new-board":
          openBoardDialog();
          break;
        case "edit-board":
          openEditBoardDialog();
          break;
        case "new-article":
          openArticleDialog();
          break;
        case "edit-article":
          openEditArticleDialog();
          break;
        case "reply":
          openReplyDialog();
          break;
        case "formal-reply":
          openFormalReplyDialog();
          break;
        case "search-rooms":
          await promptSearch("rooms");
          break;
        case "search-messages":
          await promptSearch("messages");
          break;
        case "goto-boards":
          state.level = "boards";
          state.threadIndex = 0;
          render();
          break;
        case "goto-menu":
          state.level = "menu";
          render();
          break;
        case "scroll-down":
          articleView.scrollBy({ top: articleView.clientHeight * 0.85, behavior: "smooth" });
          break;
      }
    }

    subBar.addEventListener("click", async (e) => {
      const span = e.target.closest("span[data-action]");
      if (span) {
        const action = span.getAttribute("data-action");
        await handleUIAction(action);
      }
    });

    noticeBar.addEventListener("click", async (e) => {
      const span = e.target.closest("span[data-action]");
      if (span) {
        const action = span.getAttribute("data-action");
        await handleUIAction(action);
      }
    });

    document.addEventListener("contextmenu", (event) => {
      event.preventDefault();

      // 如果點擊的是滑鼠左鍵可互動或點擊的項目，則不觸發返回
      const isClickable = event.target.closest(
        "button, a, input, textarea, select, [data-action], .menuRow, .boardRow, .threadRow, .searchRow, .modalBody, .modalFoot, .modalHead"
      );
      if (isClickable) {
        return;
      }

      if (dlgBoard && dlgBoard.open) {
        dlgBoard.close();
        return;
      }
      if (dlgArticle && dlgArticle.open) {
        dlgArticle.close();
        return;
      }
      if (dlgReply && dlgReply.open) {
        dlgReply.close();
        return;
      }
      if (dlgSearch && dlgSearch.open) {
        dlgSearch.close();
        return;
      }
      if (dlgConfirm && dlgConfirm.open) {
        dlgConfirm.close();
        return;
      }
      backPrevLevel();
    });

    let wheelAccumulator = 0;
    let lastWheelTime = 0;

    document.addEventListener("wheel", (event) => {
      if (
        (dlgBoard && dlgBoard.open) ||
        (dlgArticle && dlgArticle.open) ||
        (dlgReply && dlgReply.open) ||
        (dlgSearch && dlgSearch.open) ||
        (dlgConfirm && dlgConfirm.open)
      ) {
        return;
      }
      if (isArticleLevel()) {
        return; // 文章內文閱讀維持滑鼠原生捲動
      }

      event.preventDefault();
      const now = performance.now();
      if (now - lastWheelTime > 150) {
        wheelAccumulator = 0;
      }
      lastWheelTime = now;

      wheelAccumulator += event.deltaY;
      const threshold = 35;

      if (wheelAccumulator >= threshold) {
        const steps = Math.min(3, Math.floor(wheelAccumulator / threshold));
        wheelAccumulator %= threshold;
        move(steps);
      } else if (wheelAccumulator <= -threshold) {
        const steps = Math.min(3, Math.floor(Math.abs(wheelAccumulator) / threshold));
        wheelAccumulator = -(Math.abs(wheelAccumulator) % threshold);
        move(-steps);
      }
    }, { passive: false });

    // Touch gesture support for tablets and mobile devices
    let touchStartX = 0;
    let touchStartY = 0;
    let touchLastY = 0;
    let touchStartTime = 0;
    let isTouchDragging = false;
    let touchAccumulatorY = 0;

    document.addEventListener("touchstart", (event) => {
      if (
        (dlgBoard && dlgBoard.open) ||
        (dlgArticle && dlgArticle.open) ||
        (dlgReply && dlgReply.open) ||
        (dlgSearch && dlgSearch.open) ||
        (dlgConfirm && dlgConfirm.open)
      ) {
        return;
      }
      if (event.touches.length !== 1) return;
      const t = event.touches[0];
      touchStartX = t.clientX;
      touchStartY = t.clientY;
      touchLastY = t.clientY;
      touchStartTime = performance.now();
      isTouchDragging = false;
      touchAccumulatorY = 0;
    }, { passive: true });

    document.addEventListener("touchmove", (event) => {
      if (
        (dlgBoard && dlgBoard.open) ||
        (dlgArticle && dlgArticle.open) ||
        (dlgReply && dlgReply.open) ||
        (dlgSearch && dlgSearch.open) ||
        (dlgConfirm && dlgConfirm.open)
      ) {
        return;
      }
      if (event.touches.length !== 1) return;
      const t = event.touches[0];
      const deltaY = t.clientY - touchLastY;
      const totalDeltaX = Math.abs(t.clientX - touchStartX);
      const totalDeltaY = Math.abs(t.clientY - touchStartY);

      if (totalDeltaY > 8 || totalDeltaX > 8) {
        isTouchDragging = true;
      }

      // If in article view, allow native smooth touch scrolling for reading
      if (isArticleLevel()) {
        touchLastY = t.clientY;
        return;
      }

      // In list views (boards, threads, menu, search), vertical swipe moves selection and scrolls
      touchAccumulatorY += deltaY;
      touchLastY = t.clientY;

      const threshold = 26; // approx 1 row height
      if (touchAccumulatorY >= threshold) {
        // Finger swipes down -> pull list down (move cursor up to earlier items)
        const steps = Math.min(3, Math.floor(touchAccumulatorY / threshold));
        touchAccumulatorY %= threshold;
        move(-steps);
      } else if (touchAccumulatorY <= -threshold) {
        // Finger swipes up -> push list up (move cursor down to later items)
        const steps = Math.min(3, Math.floor(Math.abs(touchAccumulatorY) / threshold));
        touchAccumulatorY = -(Math.abs(touchAccumulatorY) % threshold);
        move(steps);
      }
    }, { passive: true });

    document.addEventListener("touchend", (event) => {
      if (
        (dlgBoard && dlgBoard.open) ||
        (dlgArticle && dlgArticle.open) ||
        (dlgReply && dlgReply.open) ||
        (dlgSearch && dlgSearch.open) ||
        (dlgConfirm && dlgConfirm.open)
      ) {
        return;
      }
      if (!isTouchDragging) return;

      // Handle horizontal swipe: swipe right to go back, swipe left to enter
      const t = event.changedTouches ? event.changedTouches[0] : null;
      if (t) {
        const deltaX = t.clientX - touchStartX;
        const deltaY = t.clientY - touchStartY;
        const duration = performance.now() - touchStartTime;
        if (Math.abs(deltaX) > 70 && Math.abs(deltaX) > Math.abs(deltaY) * 1.6 && duration < 600) {
          if (deltaX > 0) {
            // Swipe right -> Back
            if (state.level === "menu") {
              window.location.href = "/main.html";
            } else {
              backPrevLevel();
            }
          } else if (deltaX < 0 && !isArticleLevel()) {
            // Swipe left -> Enter
            enterNextLevel();
          }
        }
      }

      setTimeout(() => {
        isTouchDragging = false;
      }, 100);
    }, { passive: true });

    // Suppress accidental row clicks after a swipe gesture
    document.addEventListener("click", (event) => {
      if (isTouchDragging) {
        event.stopPropagation();
        event.preventDefault();
      }
    }, true);
