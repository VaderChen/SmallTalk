    async function init() {
      tickStatus();
      refreshStats();
      // 背景分頁不需要持續更新畫面；回到前景時再補一次最新資料。
      setInterval(() => {
        if (!document.hidden) tickStatus();
      }, 1000);
      setInterval(() => {
        if (!document.hidden) checkSessionAlive();
      }, 60000);
      setInterval(() => {
        if (!document.hidden) refreshStats();
      }, 15000);
      let refreshInFlight = false;
      const refreshVisibleThread = () => {
        if (document.hidden || refreshInFlight) return;
        refreshInFlight = true;
        refreshActiveThreadData(true).catch((error) => {
          const message = String(error?.message || error || "");
          if (message.includes("unauthorized")) {
            clearSessionAndRedirect();
          }
        }).finally(() => {
          refreshInFlight = false;
        });
      };
      setInterval(refreshVisibleThread, 15000);
      document.addEventListener("visibilitychange", () => {
        if (!document.hidden) {
          tickStatus();
          refreshStats();
          refreshVisibleThread();
        }
      }, { passive: true });
      try {
        await loadBoards();
        const requestedEntry = new URLSearchParams(window.location.search).get("entry");
        const restored = restoreState();
        if (requestedEntry === "boards" && boards.length > 0) {
          state.level = "boards";
          state.threadIndex = 0;
        } else if (!restored && boards.length > 0) {
          state.boardIndex = 0;
          state.threadIndex = 0;
          state.level = "threads";
        }
        if (state.level === "threads" || state.level === "article") {
          const page = await ensureThreadsLoaded(true);
          const lastTS = page.items.length ? page.items[page.items.length - 1].ts : "";
          markRoomRead(boards[state.boardIndex]?.room, lastTS);
          updateBoardUnread();
        }
        render(true);
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
  
