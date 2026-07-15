(function () {
  "use strict";

  var tg = window.Telegram && window.Telegram.WebApp;
  var haptic = tg && tg.HapticFeedback;

  if (tg) {
    tg.ready();
    tg.expand();
    applyThemeVars(tg.themeParams);
    tg.onEvent("themeChanged", function () { applyThemeVars(tg.themeParams); });
    if (tg.MainButton.setParams) {
      tg.MainButton.setParams({ color: "#11998e", text_color: "#ffffff" });
    }

    // True full-screen (hides Telegram's own header/chat chrome entirely) —
    // only on clients new enough to support it (Bot API 8.0+). Falls back
    // silently to the regular expanded view on older clients.
    if (typeof tg.requestFullscreen === "function") {
      try { tg.requestFullscreen(); } catch (e) { /* older client despite the feature check */ }
    }
    // Long lists (Tahlil tab, stockout groups) shouldn't accidentally close
    // the app on an overscroll swipe.
    if (typeof tg.disableVerticalSwipes === "function") tg.disableVerticalSwipes();

    applySafeAreaVars();
    tg.onEvent("safeAreaChanged", applySafeAreaVars);
    tg.onEvent("contentSafeAreaChanged", applySafeAreaVars);
    tg.onEvent("fullscreenChanged", applySafeAreaVars);
  }

  // Telegram's own floating UI (close button, etc.) in full-screen mode
  // isn't covered by the standard env(safe-area-inset-*) — Telegram reports
  // it separately via contentSafeAreaInset/safeAreaInset.
  function applySafeAreaVars() {
    var root = document.documentElement.style;
    var content = tg.contentSafeAreaInset || {};
    var device = tg.safeAreaInset || {};
    root.setProperty("--tg-safe-top", Math.max(content.top || 0, device.top || 0) + "px");
    root.setProperty("--tg-safe-bottom", Math.max(content.bottom || 0, device.bottom || 0) + "px");
  }

  function applyThemeVars(theme) {
    if (!theme) return;
    var root = document.documentElement.style;
    var map = {
      bg_color: "--tg-theme-bg-color",
      text_color: "--tg-theme-text-color",
      hint_color: "--tg-theme-hint-color",
      link_color: "--tg-theme-link-color",
      button_color: "--tg-theme-button-color",
      button_text_color: "--tg-theme-button-text-color",
      secondary_bg_color: "--tg-theme-secondary-bg-color",
    };
    Object.keys(map).forEach(function (key) {
      if (theme[key]) root.setProperty(map[key], theme[key]);
    });
  }

  function haptics(type) {
    if (!haptic) return;
    if (type === "success" || type === "error" || type === "warning") {
      haptic.notificationOccurred(type);
    } else {
      haptic.impactOccurred(type || "light");
    }
  }

  function initData() {
    return tg ? tg.initData : "";
  }

  function api(path, options) {
    options = options || {};
    options.headers = Object.assign({}, options.headers, {
      Authorization: "tma " + initData(),
    });
    if (options.body) options.headers["Content-Type"] = "application/json";
    return fetch(path, options).then(function (res) {
      if (!res.ok) throw new Error("request failed: " + res.status);
      if (res.status === 204) return null;
      return res.json();
    });
  }

  function escapeHtml(s) {
    var d = document.createElement("div");
    d.textContent = s == null ? "" : String(s);
    return d.innerHTML;
  }

  // ---------- Header date ----------

  document.getElementById("today-date").textContent = new Date().toLocaleDateString("uz-UZ", {
    weekday: "long", day: "numeric", month: "long",
  });

  // ---------- Tabs + MainButton-driven flow ----------

  var currentTab = "dashboard";
  var storeDetailOpen = false;
  var tabs = document.querySelectorAll(".tab");
  var panels = document.querySelectorAll(".panel");
  var pageTitleEl = document.getElementById("page-title");
  var pageTitles = {
    dashboard: "Boshqaruv paneli",
    stores: "Do'konlar",
    employees: "Xodimlar",
    tasks: "Vazifalar",
    tahlil: "Oy tahlili",
    "vmi-dead": "Muzlagan tovarlar",
    "vmi-risk": "Tugab qolayotgan tovarlar",
    "vmi-top": "Eng foydali tovarlar",
  };

  function switchTab(tabName) {
    if (tabName === currentTab) return;
    currentTab = tabName;
    tabs.forEach(function (t) { t.classList.toggle("active", t.dataset.tab === currentTab); });
    panels.forEach(function (p) { p.classList.toggle("active", p.id === currentTab); });
    pageTitleEl.textContent = pageTitles[currentTab] || "";
    updateMainButton();
  }

  // Both the bottom tab bar (mobile), the sidebar (desktop-width), and the
  // VMI pages' own mobile sub-nav pills share the same [data-tab] buttons,
  // so switching from any of them keeps all in sync — relevant if the
  // window crosses the responsive breakpoint while a non-default tab is open.
  tabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      if (tab.dataset.tab === currentTab) return;
      haptics("light");
      switchTab(tab.dataset.tab);
    });
  });

  // ---------- Sidebar: collapse toggle + profile footer ----------

  var sidebarCollapseBtn = document.getElementById("sidebar-collapse-btn");
  sidebarCollapseBtn.addEventListener("click", function () {
    haptics("light");
    document.body.classList.toggle("sidebar-collapsed");
  });

  function setSidebarProfile(name, photoUrl) {
    var nameEl = document.getElementById("sidebar-profile-name");
    var avatarEl = document.getElementById("sidebar-avatar");
    nameEl.textContent = name;
    if (photoUrl) {
      avatarEl.style.backgroundImage = "url(" + photoUrl + ")";
      avatarEl.textContent = "";
    } else {
      avatarEl.style.backgroundImage = "";
      avatarEl.textContent = name.charAt(0).toUpperCase();
    }
  }

  (function fillSidebarProfile() {
    var tgUser = tg && tg.initDataUnsafe && tg.initDataUnsafe.user;
    if (tgUser) {
      setSidebarProfile(tgUser.first_name || "Foydalanuvchi", tgUser.photo_url);
      return;
    }
    // Opened directly in a browser (no Telegram user object) — ask the
    // server who the current session belongs to.
    setSidebarProfile("Foydalanuvchi", null);
    api("/api/me").then(function (me) {
      if (me && me.display_name) setSidebarProfile(me.display_name, null);
    }).catch(function () { /* not logged in yet / on the login page */ });
  })();

  // Logout only makes sense for a browser session — inside Telegram there's
  // no session cookie to clear, and navigating to /login would just break
  // out of the WebView for no reason.
  function doLogout() {
    haptics("light");
    api("/api/logout", { method: "POST" }).catch(function () {}).then(function () {
      window.location.href = "/login";
    });
  }
  var sidebarLogoutBtn = document.getElementById("sidebar-logout-btn");
  var headerLogoutBtn = document.getElementById("header-logout-btn");
  if (tg) {
    sidebarLogoutBtn.hidden = true;
    headerLogoutBtn.hidden = true;
  } else {
    sidebarLogoutBtn.addEventListener("click", doLogout);
    headerLogoutBtn.addEventListener("click", doLogout);
  }

  function updateMainButton() {
    if (!tg) return;
    if (sheetOpen) {
      tg.MainButton.setText("Qo'shish");
      tg.MainButton.show();
      return;
    }
    if (storeDetailOpen) {
      tg.MainButton.hide();
      return;
    }
    if (currentTab === "tasks") {
      tg.MainButton.setText("➕ Vazifa qo'shish");
    } else {
      tg.MainButton.hide();
      return;
    }
    tg.MainButton.show();
  }

  if (tg) {
    tg.MainButton.onClick(function () {
      haptics("medium");
      if (sheetOpen) {
        submitTask();
      } else if (storeDetailOpen) {
        return;
      } else if (currentTab === "tasks") {
        openSheet();
      }
    });
  }

  // BackButton can only usefully drive one overlay at a time — track and
  // swap its handler centrally instead of stacking onClick registrations.
  var activeBackHandler = null;
  function setBackHandler(fn) {
    if (!tg) return;
    if (activeBackHandler) tg.BackButton.offClick(activeBackHandler);
    activeBackHandler = fn;
    if (fn) {
      tg.BackButton.show();
      tg.BackButton.onClick(fn);
    } else {
      tg.BackButton.hide();
    }
  }

  // ---------- Dashboard: hero + stat row + note ----------

  var heroIcon = document.getElementById("hero-icon");
  var heroValue = document.getElementById("hero-value");
  var heroLabel = document.getElementById("hero-label");
  var statRow = document.getElementById("stat-row");
  var storeList = document.getElementById("store-list");
  var storeTableBody = document.getElementById("store-table-body");
  var stockTotalValueEl = document.getElementById("stock-total-value");
  var tasksMiniCard = document.getElementById("tasks-mini-card");
  var miniStats = document.getElementById("mini-stats");

  var tileIcons = {
    tasks: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6h11M9 12h11M9 18h11"/><path d="M4 6h.01M4 12h.01M4 18h.01"/></svg>',
    calendar: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="3"/><path d="M16 2v4M8 2v4M3 10h18"/></svg>',
    clock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg>',
    warning: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/></svg>',
    money: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M9.5 15a2.5 2.5 0 0 0 2.5 1.5c1.5 0 2.5-.8 2.5-2s-1-1.7-2.5-2-2.5-.8-2.5-2 1-2 2.5-2a2.5 2.5 0 0 1 2.5 1.5M12 6.5v11"/></svg>',
    receipt: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 2h12v20l-3-2-3 2-3-2-3 2V2z"/><path d="M9 7h6M9 11h6"/></svg>',
    chart: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 3v18h18"/><path d="M7 15l4-4 3 3 5-6"/></svg>',
  };
  var tileIconDefault = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/></svg>';
  var tileColors = { tasks: "#2481cc", calendar: "#8e44ad", clock: "#e67e22", warning: "#e74c3c", money: "#16a085", receipt: "#2481cc", chart: "#8e44ad" };

  function formatMoney(n) {
    var rounded = Math.round(n || 0);
    return rounded.toString().replace(/\B(?=(\d{3})+(?!\d))/g, " ");
  }

  function appendStatChip(container, iconKey, value, label) {
    var el = document.createElement("div");
    el.className = "stat-chip";
    el.innerHTML =
      '<div class="stat-icon">' + (tileIcons[iconKey] || tileIconDefault) + "</div>" +
      '<div class="stat-value">' + escapeHtml(value) + "</div>" +
      '<div class="stat-label">' + escapeHtml(label) + "</div>";
    container.appendChild(el);
  }

  // Renders the whole dashboard: if Billz business data is present, it takes
  // over the hero card (today's revenue + store ranking) and the
  // task/calendar tiles move into a smaller secondary card. Otherwise falls
  // back to the original tiles-only layout (tasks as hero).
  function renderDashboard(data) {
    var tiles = data.tiles || [];
    var business = data.business;

    if (business) {
      heroIcon.innerHTML = tileIcons.money;
      heroValue.textContent = formatMoney(business.today_revenue) + " so'm";
      heroLabel.textContent = "Bugungi savdo (barcha do'konlar)";

      statRow.innerHTML = "";
      appendStatChip(statRow, "receipt", business.today_transactions, "Cheklar soni");
      appendStatChip(statRow, "chart", formatMoney(business.today_average_check), "O'rtacha chek");
      appendStatChip(statRow, "money", formatMoney(business.today_profit), "Foyda");

      renderStores(business.stores);
      stockTotalValueEl.textContent = formatMoney(business.total_stock_quantity) + " dona · " +
        formatMoney(business.total_stock_value) + " so'm";

      renderMiniStats(tiles);
      tasksMiniCard.hidden = tiles.length === 0;

      renderPaymentBalance();
    } else {
      renderTilesAsHero(tiles);
      renderStores([]);
      tasksMiniCard.hidden = true;
      paymentBalanceCard.hidden = true;
    }
  }

  // Fallback layout (no Billz configured): first tile is the hero, the rest
  // fill the stat row — same behaviour as before the business dashboard.
  function renderTilesAsHero(tiles) {
    var hero = tiles[0];
    var rest = tiles.slice(1);

    if (hero) {
      heroIcon.innerHTML = tileIcons[hero.icon] || tileIconDefault;
      heroValue.textContent = hero.value;
      heroLabel.textContent = hero.label;
    }

    statRow.innerHTML = "";
    rest.forEach(function (tile) { appendStatChip(statRow, tile.icon, tile.value, tile.label); });
  }

  function renderMiniStats(tiles) {
    miniStats.innerHTML = "";
    tiles.forEach(function (tile) {
      var el = document.createElement("div");
      el.className = "mini-stat";
      if (tileColors[tile.icon]) el.style.setProperty("--tile-color", tileColors[tile.icon]);
      el.innerHTML =
        '<div class="mini-stat-icon">' + (tileIcons[tile.icon] || tileIconDefault) + "</div>" +
        '<div class="mini-stat-value">' + escapeHtml(tile.value) + "</div>" +
        '<div class="mini-stat-label">' + escapeHtml(tile.label) + "</div>";
      miniStats.appendChild(el);
    });
  }

  var chevronIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 18l6-6-6-6"/></svg>';

  var storeListCollapsedCount = 5;

  function buildStoreRow(store) {
    var li = document.createElement("li");
    li.className = "store-item";
    var barWidth = Math.max(store.share_percent || 0, store.revenue > 0 ? 2 : 0);
    li.innerHTML =
      '<div class="store-row"><span class="store-name">' + escapeHtml(store.name) + "</span>" +
      '<span class="store-revenue">' + formatMoney(store.revenue) + "</span>" +
      '<span class="store-row-chevron">' + chevronIcon + "</span></div>" +
      '<div class="store-bar-track"><div class="store-bar-fill" style="width:' + barWidth + '%"></div></div>' +
      '<div class="store-meta">' + (store.transactions || 0) + " ta chek · o'rtacha " + formatMoney(store.average_check) +
      " · omborda " + formatMoney(store.stock_quantity) + " dona</div>";
    if (store.id) {
      li.addEventListener("click", function () { openStoreDetail(store.id, store.name); });
    }
    return li;
  }

  function renderStores(stores) {
    stores = stores || [];
    storeList.innerHTML = "";
    var visible = stores.slice(0, storeListCollapsedCount);
    var rest = stores.slice(storeListCollapsedCount);

    visible.forEach(function (store) { storeList.appendChild(buildStoreRow(store)); });

    if (rest.length > 0) {
      var toggle = document.createElement("li");
      toggle.className = "store-list-toggle";
      toggle.textContent = "Yana " + rest.length + " ta do'kon";
      toggle.addEventListener("click", function () {
        rest.forEach(function (store) { storeList.appendChild(buildStoreRow(store)); });
        toggle.remove();
      });
      storeList.appendChild(toggle);
    }

    renderStoreTable(stores);
  }

  // Desktop-width companion to renderStores — a real table (all stores,
  // no collapsing needed since a table reads fine at any length with a
  // fixed-height scroll area). CSS shows only one of the two per viewport.
  function renderStoreTable(stores) {
    storeTableBody.innerHTML = "";
    stores.forEach(function (store) {
      var tr = document.createElement("tr");
      tr.innerHTML =
        "<td>" + escapeHtml(store.name) + "</td>" +
        "<td>" + formatMoney(store.revenue) + " so'm</td>" +
        "<td>" + (store.share_percent || 0).toFixed(1) + "%</td>" +
        "<td>" + (store.transactions || 0) + "</td>" +
        "<td>" + formatMoney(store.average_check) + " so'm</td>" +
        "<td>" + formatMoney(store.stock_quantity) + "</td>" +
        "<td>" + formatMoney(store.stock_value) + " so'm</td>";
      if (store.id) {
        tr.addEventListener("click", function () { openStoreDetail(store.id, store.name); });
      }
      storeTableBody.appendChild(tr);
    });
  }

  // ---------- Xodimlar (Employees) module — company-wide seller leaderboard ----------

  var employeesTableBody = document.getElementById("employees-table-body");
  var employeesLoaded = false;

  function loadEmployees() {
    if (employeesLoaded) return;
    employeesLoaded = true;
    api("/api/employees")
      .then(function (rows) {
        employeesTableBody.innerHTML = "";
        if (!rows || rows.length === 0) {
          employeesTableBody.innerHTML = '<tr><td colspan="3">Ma\'lumot yo\'q</td></tr>';
          return;
        }
        rows.forEach(function (row) {
          var tr = document.createElement("tr");
          tr.innerHTML =
            "<td>" + escapeHtml(row.name) + "</td>" +
            "<td>" + formatMoney(row.items_sold) + "</td>" +
            "<td>" + formatMoney(row.revenue) + " so'm</td>";
          employeesTableBody.appendChild(tr);
        });
      })
      .catch(function () {
        employeesTableBody.innerHTML = '<tr><td colspan="3">Yuklab bo\'lmadi.</td></tr>';
      });
  }

  var paymentBalanceCard = document.getElementById("balance-card");
  var balanceTableBody = document.getElementById("balance-table-body");

  // Balance by payment method (CLICK, Payme, cash, etc) — a plain table,
  // not a chart, since the point is reading exact amounts per method.
  function renderPaymentBalance() {
    api("/api/analytics/payments")
      .then(function (items) {
        paymentBalanceCard.hidden = !items || items.length === 0;
        balanceTableBody.innerHTML = "";
        (items || []).forEach(function (it) {
          var tr = document.createElement("tr");
          tr.innerHTML =
            "<td>" + escapeHtml(it.name) + "</td>" +
            '<td><span class="vmi-emphasis">' + formatMoney(it.sum) + " so'm</span>" +
            '<div class="balance-percent">' + it.percent.toFixed(1) + "%</div></td>";
          balanceTableBody.appendChild(tr);
        });
      })
      .catch(function () {
        paymentBalanceCard.hidden = false;
        balanceTableBody.innerHTML = '<tr><td colspan="2">Yuklab bo\'lmadi.</td></tr>';
      });
  }

  function loadDashboard() {
    api("/api/dashboard").then(renderDashboard);
  }

  // ---------- Tasks: filter chips + list ----------

  var taskListEl = document.getElementById("task-list");
  var filterChips = document.querySelectorAll(".filter-chip");
  var currentFilter = "all";
  var allTasks = [];

  var taskIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>';
  var warningIcon = tileIcons.warning;
  var clockIcon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg>';

  filterChips.forEach(function (chip) {
    chip.addEventListener("click", function () {
      if (chip.dataset.filter === currentFilter) return;
      haptics("light");
      currentFilter = chip.dataset.filter;
      filterChips.forEach(function (c) { c.classList.toggle("active", c === chip); });
      renderTasks(applyFilter(allTasks));
    });
  });

  function applyFilter(list) {
    if (!list) return [];
    var now = new Date();
    if (currentFilter === "today") {
      return list.filter(function (t) {
        if (!t.due_at) return false;
        var d = new Date(t.due_at);
        return d.toDateString() === now.toDateString();
      });
    }
    if (currentFilter === "overdue") {
      return list.filter(function (t) { return t.due_at && new Date(t.due_at) < now; });
    }
    return list;
  }

  function formatDue(dueAt) {
    if (!dueAt) return "";
    var d = new Date(dueAt);
    return d.toLocaleString("uz-UZ", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
  }

  function renderTasks(list) {
    taskListEl.innerHTML = "";
    if (!list || list.length === 0) {
      taskListEl.innerHTML =
        '<li class="empty-state"><span class="emoji">🎉</span><span class="msg">Bu yerda vazifalar yo\'q</span></li>';
      return;
    }
    var now = new Date();
    list.forEach(function (t) {
      var overdue = t.due_at && new Date(t.due_at) < now;

      var li = document.createElement("li");
      li.className = "task-item";

      var icon = document.createElement("div");
      icon.className = "task-icon" + (overdue ? " overdue" : "");
      icon.innerHTML = overdue ? warningIcon : taskIcon;

      var body = document.createElement("div");
      body.className = "task-body";

      var desc = document.createElement("span");
      desc.className = "task-desc";
      desc.textContent = t.description;
      body.appendChild(desc);

      if (t.due_at) {
        var due = document.createElement("span");
        due.className = "task-due" + (overdue ? " overdue" : "");
        due.innerHTML = clockIcon + "<span>" + formatDue(t.due_at) + "</span>";
        body.appendChild(due);
      }

      var toggle = document.createElement("button");
      toggle.className = "toggle-switch";
      toggle.setAttribute("aria-label", "Bajarildi deb belgilash");
      toggle.innerHTML = '<span class="toggle-knob"></span>';
      toggle.addEventListener("click", function () { completeTask(t.id, li, toggle); });

      li.appendChild(icon);
      li.appendChild(body);
      li.appendChild(toggle);
      taskListEl.appendChild(li);
    });
  }

  function loadTasks() {
    api("/api/tasks").then(function (list) {
      allTasks = list || [];
      renderTasks(applyFilter(allTasks));
    }).catch(function () {
      taskListEl.innerHTML = '<li class="empty-state"><span class="msg">Vazifalarni yuklab bo\'lmadi.</span></li>';
    });
  }

  function completeTask(id, li, toggle) {
    haptics("light");
    toggle.classList.add("on");
    li.classList.add("completing");
    api("/api/tasks/" + id + "/complete", { method: "POST" })
      .then(function () {
        haptics("success");
        setTimeout(loadTasks, 220);
      })
      .catch(function () {
        toggle.classList.remove("on");
        li.classList.remove("completing");
        haptics("error");
      });
  }

  // ---------- Add-task bottom sheet ----------

  var sheetOpen = false;
  var sheet = document.getElementById("add-task-sheet");
  var backdrop = document.getElementById("sheet-backdrop");
  var descInput = document.getElementById("task-description");
  var dueInput = document.getElementById("task-due");

  function openSheet() {
    sheetOpen = true;
    sheet.classList.add("open");
    backdrop.classList.add("open");
    updateMainButton();
    setBackHandler(closeSheet);
    setTimeout(function () { descInput.focus(); }, 250);
  }

  function closeSheet() {
    sheetOpen = false;
    sheet.classList.remove("open");
    backdrop.classList.remove("open");
    descInput.value = "";
    dueInput.value = "";
    setBackHandler(null);
    updateMainButton();
  }

  backdrop.addEventListener("click", closeSheet);

  function submitTask() {
    if (!descInput.value.trim()) {
      haptics("error");
      descInput.focus();
      return;
    }
    var body = { description: descInput.value.trim() };
    if (dueInput.value) body.due_at = new Date(dueInput.value).toISOString();

    api("/api/tasks", { method: "POST", body: JSON.stringify(body) })
      .then(function () {
        haptics("success");
        closeSheet();
        switchTab("tasks");
        loadTasks();
      })
      .catch(function () { haptics("error"); });
  }

  document.getElementById("add-task-form").addEventListener("submit", function (e) {
    e.preventDefault();
    submitTask();
  });

  // ---------- Tahlil tab: month-to-month + stockout ----------

  var chartIdCounter = 0;

  // Minimal hand-rolled SVG bar chart — no charting library, matches the
  // project's no-build-step constraint. `points` is [{label, value}, ...].
  function renderBarChart(container, points) {
    if (!points || points.length === 0) {
      container.innerHTML = '<div class="chart-empty">Ma\'lumot yo\'q</div>';
      return;
    }
    chartIdCounter += 1;
    var gradId = "barGrad" + chartIdCounter;

    var w = 300, h = 84, gap = 6;
    var maxVal = Math.max.apply(null, points.map(function (p) { return p.value; }).concat([1]));
    var barW = (w - gap * (points.length - 1)) / points.length;

    var bars = points.map(function (p, i) {
      var barH = maxVal > 0 ? (Math.max(p.value, 0) / maxVal) * (h - 22) : 0;
      var x = i * (barW + gap);
      var y = h - barH - 16;
      return (
        '<rect x="' + x.toFixed(1) + '" y="' + y.toFixed(1) + '" width="' + barW.toFixed(1) +
        '" height="' + Math.max(barH, 1).toFixed(1) + '" rx="3" fill="url(#' + gradId + ')"></rect>' +
        '<text x="' + (x + barW / 2).toFixed(1) + '" y="' + (h - 4) +
        '" font-size="8" text-anchor="middle" fill="var(--hint)">' + escapeHtml(p.label) + "</text>"
      );
    }).join("");

    container.innerHTML =
      '<svg viewBox="0 0 ' + w + " " + h + '" preserveAspectRatio="none">' +
      '<defs><linearGradient id="' + gradId + '" x1="0" y1="0" x2="0" y2="1">' +
      '<stop offset="0%" stop-color="#38ef7d"/><stop offset="100%" stop-color="#11998e"/>' +
      "</linearGradient></defs>" + bars + "</svg>";
  }

  function fillMonthlyComparison(data, els) {
    els.thisLabel.textContent = data.this_month.label || "Bu oy";
    els.thisValue.textContent = formatMoney(data.this_month.revenue) + " so'm";
    els.lastLabel.textContent = data.last_year.label || "O'tgan yil";
    els.lastValue.textContent = formatMoney(data.last_year.revenue) + " so'm";

    if (data.change_percent === null || data.change_percent === undefined) {
      els.trend.hidden = true;
    } else {
      var pct = data.change_percent;
      els.trend.hidden = false;
      els.trend.classList.toggle("down", pct < 0);
      els.trend.textContent = (pct >= 0 ? "▲ " : "▼ ") + Math.abs(pct).toFixed(1) + "%";
    }

    renderBarChart(els.chart, [
      { label: "O'tgan yil", value: data.last_year.revenue || 0 },
      { label: "Bu oy", value: data.this_month.revenue || 0 },
    ]);
  }

  var monthlyEls = {
    thisLabel: document.getElementById("monthly-this-label"),
    thisValue: document.getElementById("monthly-this-value"),
    lastLabel: document.getElementById("monthly-last-label"),
    lastValue: document.getElementById("monthly-last-value"),
    trend: document.getElementById("monthly-trend"),
    chart: document.getElementById("monthly-chart"),
  };
  var vmiDeadListEl = document.getElementById("vmi-dead-list");
  var vmiRiskListEl = document.getElementById("vmi-risk-list");
  var vmiTopListEl = document.getElementById("vmi-top-list");

  function vmiNameCell(it) {
    return '<div class="vmi-table-name">' + escapeHtml(it.product_name) + '</div><div class="vmi-table-sku">' + escapeHtml(it.sku) + "</div>";
  }

  function vmiStatusBadge(it) {
    if (it.category === "out") return '<span class="stockout-badge status-out">Tugagan</span>';
    var days = it.days_remaining != null ? Math.max(Math.round(it.days_remaining), 0) : null;
    return '<span class="stockout-badge status-low">' + (days != null ? days + " kun qoldi" : "Kam qoldi") + "</span>";
  }

  // A real <table> per VMI page — sortable-looking columns, horizontal
  // scroll on narrow screens rather than squeezed/truncated cells.
  function buildVMITable(items, columns) {
    var wrap = document.createElement("div");
    wrap.className = "vmi-table-wrap";
    var table = document.createElement("table");
    table.className = "vmi-table";

    var thead = document.createElement("thead");
    var headRow = document.createElement("tr");
    columns.forEach(function (col) {
      var th = document.createElement("th");
      th.textContent = col.label;
      headRow.appendChild(th);
    });
    thead.appendChild(headRow);
    table.appendChild(thead);

    var tbody = document.createElement("tbody");
    items.forEach(function (it) {
      var tr = document.createElement("tr");
      columns.forEach(function (col) {
        var td = document.createElement("td");
        td.innerHTML = col.render(it);
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
    return wrap;
  }

  var vmiDeadColumns = [
    { label: "Tovar", render: vmiNameCell },
    { label: "Zaxira", render: function (it) { return formatMoney(it.total_stock) + " dona"; } },
    { label: "Chakana narx", render: function (it) { return it.retail_price ? formatMoney(it.retail_price) + " so'm" : "–"; } },
    { label: "Muzlagan mablag'", render: function (it) { return '<span class="vmi-emphasis">' + formatMoney(it.frozen_value) + " so'm</span>"; } },
  ];

  var vmiRiskColumns = [
    { label: "Tovar", render: vmiNameCell },
    { label: "Holat", render: vmiStatusBadge },
    { label: "Zaxira", render: function (it) { return formatMoney(it.total_stock) + " dona"; } },
    { label: "Kunlik sotilish", render: function (it) { return it.daily_velocity ? it.daily_velocity.toFixed(2) + " dona/kun" : "–"; } },
    { label: "Dona foyda", render: function (it) { return it.profit_per_unit ? formatMoney(it.profit_per_unit) + " so'm" : "–"; } },
    { label: "Kunlik xavf (tugasa)", render: function (it) { return it.potential_daily_profit_loss ? '<span class="vmi-emphasis danger">' + formatMoney(it.potential_daily_profit_loss) + " so'm</span>" : "–"; } },
  ];

  var vmiTopColumns = [
    { label: "Tovar", render: vmiNameCell },
    { label: "Zaxira", render: function (it) { return formatMoney(it.total_stock) + " dona"; } },
    { label: "Kunlik sotilish", render: function (it) { return it.daily_velocity ? it.daily_velocity.toFixed(2) + " dona/kun" : "–"; } },
    { label: "30 kunlik savdo", render: function (it) { return formatMoney(it.gross_sales_30d) + " so'm"; } },
    { label: "30 kunlik foyda", render: function (it) { return '<span class="vmi-emphasis">' + formatMoney(it.net_profit_30d) + " so'm</span>"; } },
  ];

  function renderVMIDeadList(items) {
    vmiDeadListEl.innerHTML = "";
    if (!items || items.length === 0) {
      vmiDeadListEl.innerHTML = '<div class="empty-state"><span class="emoji">✅</span><span class="msg">Muzlagan tovar yo\'q</span></div>';
      return;
    }
    vmiDeadListEl.appendChild(buildVMITable(items, vmiDeadColumns));
  }

  function renderVMIRiskList(items) {
    vmiRiskListEl.innerHTML = "";
    if (!items || items.length === 0) {
      vmiRiskListEl.innerHTML = '<div class="empty-state"><span class="emoji">✅</span><span class="msg">Diqqat talab qiladigan tovar yo\'q</span></div>';
      return;
    }
    vmiRiskListEl.appendChild(buildVMITable(items, vmiRiskColumns));
  }

  function renderVMITopList(items) {
    vmiTopListEl.innerHTML = "";
    if (!items || items.length === 0) {
      vmiTopListEl.innerHTML = '<div class="empty-state"><span class="msg">Ma\'lumot yo\'q</span></div>';
      document.getElementById("vmi-top-total-value").textContent = "–";
      document.getElementById("vmi-top-count").textContent = "0";
      return;
    }
    vmiTopListEl.appendChild(buildVMITable(items, vmiTopColumns));

    var total = items.reduce(function (sum, it) { return sum + (it.net_profit_30d || 0); }, 0);
    document.getElementById("vmi-top-total-value").textContent = formatMoney(total) + " so'm";
    document.getElementById("vmi-top-count").textContent = items.length;
  }

  function renderVMISummary(summary) {
    document.getElementById("vmi-frozen-value").textContent = formatMoney(summary.total_frozen_capital) + " so'm";
    document.getElementById("vmi-dead-count").textContent = summary.dead_sku_count || 0;
    document.getElementById("vmi-risk-value").textContent = formatMoney(summary.total_potential_daily_profit_loss) + " so'm";
    document.getElementById("vmi-risk-count").textContent = summary.at_risk_sku_count || 0;
  }

  // ---------- Category pie chart (hand-rolled, no charting library) ----------

  var categoryChartEl = document.getElementById("category-chart");
  var categoryLegendEl = document.getElementById("category-legend");
  var pieColors = ["#11998e", "#38ef7d", "#2481cc", "#8e44ad", "#e67e22", "#8e8e93", "#e74c3c", "#f1c40f"];

  function renderPieChart(container, legendEl, items) {
    items = (items || []).filter(function (it) { return it.revenue > 0; });
    if (items.length === 0) {
      container.innerHTML = '<div class="chart-empty">Ma\'lumot yo\'q</div>';
      legendEl.innerHTML = "";
      return;
    }

    var r = 45, cx = 60, cy = 60, stroke = 18;
    var circumference = 2 * Math.PI * r;
    var offset = 0;
    var segments = items.map(function (it, i) {
      var dash = Math.max((it.percent / 100) * circumference, 0);
      var seg =
        '<circle cx="' + cx + '" cy="' + cy + '" r="' + r + '" fill="none" stroke="' + pieColors[i % pieColors.length] +
        '" stroke-width="' + stroke + '" stroke-dasharray="' + dash.toFixed(1) + " " + (circumference - dash).toFixed(1) +
        '" stroke-dashoffset="' + (-offset).toFixed(1) + '"></circle>';
      offset += dash;
      return seg;
    }).join("");

    container.innerHTML =
      '<svg viewBox="0 0 120 120"><g transform="rotate(-90 ' + cx + " " + cy + ')">' + segments + "</g></svg>";

    legendEl.innerHTML = "";
    items.forEach(function (it, i) {
      var row = document.createElement("li");
      row.className = "legend-item";
      var valueText = it.percent.toFixed(1) + "%";
      row.innerHTML =
        '<span class="legend-dot" style="background:' + pieColors[i % pieColors.length] + '"></span>' +
        '<span class="legend-name">' + escapeHtml(it.name) + "</span>" +
        '<span class="legend-value">' + valueText + "</span>";
      legendEl.appendChild(row);
    });
  }

  // ---------- Forecast card ----------

  var forecastEls = {
    value: document.getElementById("forecast-value"),
    note: document.getElementById("forecast-note"),
    trend: document.getElementById("forecast-trend"),
  };

  function renderForecast(data) {
    if (!data || !data.projected_total) {
      forecastEls.value.textContent = "–";
      forecastEls.note.textContent = "Ma'lumot yo'q";
      forecastEls.trend.hidden = true;
      return;
    }
    forecastEls.value.textContent = formatMoney(data.projected_total) + " so'm";
    forecastEls.note.textContent =
      "Hozircha: " + formatMoney(data.month_to_date_revenue) + " so'm · yana " + data.days_remaining + " kun qoldi";

    if (data.change_percent === null || data.change_percent === undefined) {
      forecastEls.trend.hidden = true;
    } else {
      var pct = data.change_percent;
      forecastEls.trend.hidden = false;
      forecastEls.trend.classList.toggle("down", pct < 0);
      forecastEls.trend.textContent = (pct >= 0 ? "▲ " : "▼ ") + Math.abs(pct).toFixed(1) + "%";
    }
  }

  var tahlilLoaded = false;
  function loadTahlil() {
    if (tahlilLoaded) return;
    tahlilLoaded = true;

    api("/api/analytics/monthly")
      .then(function (data) {
        if (data && data.this_month) fillMonthlyComparison(data, monthlyEls);
      })
      .catch(function () { renderBarChart(monthlyEls.chart, []); });

    api("/api/analytics/forecast")
      .then(function (data) { renderForecast(data); })
      .catch(function () {
        forecastEls.value.textContent = "–";
        forecastEls.note.textContent = "Yuklab bo'lmadi.";
      });

    api("/api/analytics/categories")
      .then(function (items) { renderPieChart(categoryChartEl, categoryLegendEl, items); })
      .catch(function () { categoryChartEl.innerHTML = '<div class="chart-empty">Yuklab bo\'lmadi.</div>'; });
  }

  var vmiLoaded = false;
  function loadVMI() {
    if (vmiLoaded) return;
    vmiLoaded = true;

    api("/api/analytics/vmi")
      .then(function (data) {
        renderVMISummary(data.summary || {});
        renderVMIDeadList(data.dead_stock);
        renderVMIRiskList(data.at_risk);
        renderVMITopList(data.top_sellers);
      })
      .catch(function () {
        vmiDeadListEl.innerHTML = '<div class="empty-state"><span class="msg">Yuklab bo\'lmadi.</span></div>';
        vmiRiskListEl.innerHTML = '<div class="empty-state"><span class="msg">Yuklab bo\'lmadi.</span></div>';
        vmiTopListEl.innerHTML = '<div class="empty-state"><span class="msg">Yuklab bo\'lmadi.</span></div>';
      });
  }

  // ---------- Store detail full panel ----------

  var storeDetailBackdrop = document.getElementById("store-detail-backdrop");
  var storeDetailPanel = document.getElementById("store-detail-panel");
  var storeDetailNameEl = document.getElementById("store-detail-name");
  var storeTrendChart = document.getElementById("store-trend-chart");
  var storeTopProductsEl = document.getElementById("store-top-products");
  var storeTopSellersEl = document.getElementById("store-top-sellers");
  var storeMonthlyEls = {
    thisLabel: document.getElementById("store-monthly-this-label"),
    thisValue: document.getElementById("store-monthly-this-value"),
    lastLabel: document.getElementById("store-monthly-last-label"),
    lastValue: document.getElementById("store-monthly-last-value"),
    trend: document.getElementById("store-monthly-trend"),
  };

  // Guards against a slow/stale request (e.g. the user taps a second store
  // before the first one's data comes back) clobbering a newer view.
  var storeDetailRequestID = 0;

  function openStoreDetail(storeId, storeName) {
    var requestID = ++storeDetailRequestID;

    storeDetailOpen = true;
    storeDetailNameEl.textContent = storeName || "Do'kon";
    storeTrendChart.innerHTML = '<div class="chart-empty">Yuklanmoqda...</div>';
    storeTopProductsEl.innerHTML = "";
    storeTopSellersEl.innerHTML = "";
    storeDetailBackdrop.classList.add("open");
    storeDetailPanel.classList.add("open");
    setBackHandler(closeStoreDetail);
    updateMainButton();
    haptics("light");

    api("/api/analytics/store/" + encodeURIComponent(storeId))
      .then(function (data) {
        if (requestID !== storeDetailRequestID) return; // a newer store was opened meanwhile

        try {
          renderBarChart(storeTrendChart, (data.daily_trend || []).map(function (p) {
            return { label: p.date, value: p.revenue };
          }));
        } catch (e) {
          storeTrendChart.innerHTML = '<div class="chart-empty">Grafikni chizib bo\'lmadi.</div>';
        }

        try {
          fillMonthlyComparison({
            this_month: data.this_month,
            last_year: data.last_year,
            change_percent: data.change_percent,
          }, storeMonthlyEls);
        } catch (e) { /* leave previous values in place */ }

        try {
          storeTopProductsEl.innerHTML = "";
          if (!data.top_products || data.top_products.length === 0) {
            storeTopProductsEl.innerHTML =
              '<li class="empty-state"><span class="msg">Bu oy savdo bo\'lmagan</span></li>';
          } else {
            data.top_products.forEach(function (p) {
              var li = document.createElement("li");
              li.className = "product-item";
              li.innerHTML =
                '<span class="product-name">' + escapeHtml(p.name) + "</span>" +
                '<span class="product-figures">' + formatMoney(p.sold) + " dona · " + formatMoney(p.revenue) + " so'm</span>";
              storeTopProductsEl.appendChild(li);
            });
          }
        } catch (e) {
          storeTopProductsEl.innerHTML = '<li class="empty-state"><span class="msg">Yuklab bo\'lmadi.</span></li>';
        }

        try {
          storeTopSellersEl.innerHTML = "";
          if (!data.top_sellers || data.top_sellers.length === 0) {
            storeTopSellersEl.innerHTML =
              '<li class="empty-state"><span class="msg">Bu oy savdo bo\'lmagan</span></li>';
          } else {
            data.top_sellers.forEach(function (p) {
              var li = document.createElement("li");
              li.className = "product-item";
              li.innerHTML =
                '<span class="product-name">' + escapeHtml(p.name) + "</span>" +
                '<span class="product-figures">' + formatMoney(p.items_sold) + " dona · " + formatMoney(p.revenue) + " so'm</span>";
              storeTopSellersEl.appendChild(li);
            });
          }
        } catch (e) {
          storeTopSellersEl.innerHTML = '<li class="empty-state"><span class="msg">Yuklab bo\'lmadi.</span></li>';
        }
      })
      .catch(function () {
        if (requestID !== storeDetailRequestID) return;
        storeTrendChart.innerHTML = '<div class="chart-empty">Yuklab bo\'lmadi.</div>';
      });
  }

  function closeStoreDetail() {
    storeDetailOpen = false;
    storeDetailBackdrop.classList.remove("open");
    storeDetailPanel.classList.remove("open");
    setBackHandler(null);
    updateMainButton();
  }

  storeDetailBackdrop.addEventListener("click", closeStoreDetail);

  // ---------- Init ----------

  updateMainButton();
  loadDashboard();
  loadEmployees();
  loadTasks();
  loadTahlil();
  loadVMI();
})();
