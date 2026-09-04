    async function init() {
      tickStatus();
      refreshStats();
      // 背景分頁不需要持續更新畫面；回到前景時再補一次最新資料。
      setInterval(() => {
        if (!document.hidden) tickStatus();
      }, 1000);
	  let refreshInFlight = false;
      let refreshCycleCount = 0;
      const refreshVisibleData = async () => {
        if (document.hidden || refreshInFlight) return;
        refreshInFlight = true;
        refreshCycleCount++;
        try {
          if (state.level === "threads" || state.level === "article") {
            await refreshActiveThreadData(true);
            if (refreshCycleCount % 2 === 0) {
              await refreshBoardsData();
            }
          } else {
            await refreshBoardsData();
          }
        } catch (error) {
          console.warn("背景更新失敗，保留目前頁面內容：", error);
        } finally {
          refreshInFlight = false;
        }
      };
      let refreshTimer = 0;
      const scheduleRefresh = (delay) => {
        window.clearTimeout(refreshTimer);
        refreshTimer = window.setTimeout(runRefreshCycle, delay);
      };
      const runRefreshCycle = async () => {
        if (document.hidden) {
          scheduleRefresh(15000);
          return;
        }
        try {
          await Promise.all([refreshStats(), refreshVisibleData()]);
          scheduleRefresh(state.level === "threads" || state.level === "article" ? 5000 : 15000);
        } catch (error) {
          scheduleRefresh(30000);
        }
      };
      scheduleRefresh(5000);

	  document.addEventListener("visibilitychange", () => {
		if (!document.hidden) {
		  tickStatus();
		  scheduleRefresh(0);
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
  
