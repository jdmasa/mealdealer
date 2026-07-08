// Frontend logic for the meal-menu planner. Vanilla JS, no build step.
(function () {
  const t = (k) => window.I18N.t(k);

  // ---- tiny DOM helpers -------------------------------------------------
  function el(tag, attrs, children) {
    const node = document.createElement(tag);
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (k === "class") node.className = v;
        else if (k === "text") node.textContent = v;
        else if (k.startsWith("on") && typeof v === "function")
          node.addEventListener(k.slice(2), v);
        else node.setAttribute(k, v);
      }
    }
    for (const c of children || []) {
      if (c == null) continue;
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    }
    return node;
  }
  const $ = (id) => document.getElementById(id);

  // ---- API --------------------------------------------------------------
  async function api(method, path, body, isForm) {
    const opts = { method, headers: {} };
    if (isForm) {
      opts.body = body;
    } else if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    if (res.status === 204) return null;
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function banner(msg, kind) {
    const b = $("banner");
    b.textContent = msg;
    b.className = "banner " + (kind || "info");
    b.classList.remove("hidden");
    if (kind !== "error") {
      setTimeout(() => b.classList.add("hidden"), 4000);
    }
  }
  const showError = (e) => banner(t("errorPrefix") + ": " + (e.message || e), "error");

  // ---- week model helpers ----------------------------------------------
  function emptyWeek() {
    return {
      title: "",
      weekStart: "",
      days: window.I18N.dayKeys.map((name) => ({
        name,
        lunch: { dish: "", notes: "" },
        dinner: { dish: "", notes: "" },
      })),
    };
  }

  function normalizeWeek(w) {
    const base = emptyWeek();
    if (!w) return base;
    base.id = w.id;
    base.title = w.title || "";
    base.weekStart = w.weekStart || "";
    if (Array.isArray(w.days)) {
      for (let i = 0; i < 7 && i < w.days.length; i++) {
        const d = w.days[i] || {};
        base.days[i].lunch = { dish: (d.lunch && d.lunch.dish) || "", notes: (d.lunch && d.lunch.notes) || "" };
        base.days[i].dinner = { dish: (d.dinner && d.dinner.dish) || "", notes: (d.dinner && d.dinner.notes) || "" };
      }
    }
    return base;
  }

  // Renders an editable 7x2 grid bound live to `week`. Re-callable on re-render.
  function renderWeekEditor(container, week, opts) {
    opts = opts || {};
    container.innerHTML = "";

    const titleField = el("div", { class: "field" }, [
      el("label", { text: t("titleLabel") }),
      (function () {
        const input = el("input", { type: "text", value: week.title || "" });
        input.addEventListener("input", () => (week.title = input.value));
        return input;
      })(),
    ]);
    container.appendChild(titleField);

    const grid = el("div", { class: "week-grid" });
    // header row
    grid.appendChild(el("div", { class: "gh" }, [""]));
    grid.appendChild(el("div", { class: "gh", text: t("lunch") }));
    grid.appendChild(el("div", { class: "gh", text: t("dinner") }));

    week.days.forEach((day, i) => {
      grid.appendChild(el("div", { class: "day-name", text: window.I18N.dayLabel(i) }));
      grid.appendChild(mealCell(day.lunch));
      grid.appendChild(mealCell(day.dinner));
    });
    container.appendChild(grid);

    if (opts.onSave) {
      const btn = el("button", { class: "primary", text: opts.saveLabel || t("saveBtn") });
      btn.addEventListener("click", () => opts.onSave(week, btn));
      container.appendChild(el("div", { class: "actions" }, [btn]));
    }
  }

  function mealCell(meal) {
    const dish = el("input", { type: "text", placeholder: t("dish"), value: meal.dish || "" });
    dish.addEventListener("input", () => (meal.dish = dish.value));
    const notes = el("input", { type: "text", placeholder: t("notes"), value: meal.notes || "" });
    notes.addEventListener("input", () => (meal.notes = notes.value));
    return el("div", { class: "meal-cell" }, [dish, notes]);
  }

  const tf = (key, params) => {
    let s = t(key);
    for (const k in params) s = s.split("{" + k + "}").join(params[k]);
    return s;
  };

  // ISO date "YYYY-MM-DD" + n days, computed in UTC to avoid TZ drift.
  function addDaysISO(iso, n) {
    const [y, m, d] = iso.split("-").map(Number);
    const dt = new Date(Date.UTC(y, m - 1, d));
    dt.setUTCDate(dt.getUTCDate() + n);
    return dt.toISOString().slice(0, 10);
  }

  function combineLunch(x) {
    const notes = (x.notes || "").trim();
    return notes ? (x.dish || "").trim() + " · " + notes : (x.dish || "").trim();
  }

  // ---- SUGGEST tab ------------------------------------------------------
  const suggestState = { lunches: ["", "", "", "", "", "", ""], week: null, datedLunches: [] };

  function renderLunchInputs() {
    const wrap = $("lunchInputs");
    wrap.innerHTML = "";
    for (let i = 0; i < 7; i++) {
      const input = el("input", {
        type: "text",
        value: suggestState.lunches[i] || "",
        placeholder: window.I18N.dayLabel(i),
      });
      input.addEventListener("input", () => (suggestState.lunches[i] = input.value));
      wrap.appendChild(
        el("div", { class: "lunch-row" }, [
          el("span", { class: "lunch-day", text: window.I18N.dayLabel(i) }),
          input,
        ])
      );
    }
  }

  async function extractLunchesFile(btn) {
    const f = $("lunchFile").files[0];
    if (!f) return;
    const original = btn.textContent;
    btn.disabled = true;
    btn.textContent = t("extracting");
    try {
      const fd = new FormData();
      fd.append("file", f);
      const res = await api("POST", "/api/lunches/extract", fd, true);
      suggestState.datedLunches = (res && res.lunches) || [];
      $("lunchLoadedInfo").textContent = tf("lunchLoaded", { n: suggestState.datedLunches.length });
      fillLunchesFromDate();
    } catch (e) {
      showError(e);
    } finally {
      btn.disabled = false;
      btn.textContent = original;
    }
  }

  // Maps the uploaded dated lunches onto the 7 day-slots of the target week.
  function fillLunchesFromDate() {
    const dl = suggestState.datedLunches;
    if (!dl || dl.length === 0) return;

    let start = $("suggestDate").value;
    if (!start) {
      // Default to the earliest covered date (canteen weeks start on Monday).
      const dates = dl.map((x) => x.date).filter(Boolean).sort();
      if (dates.length) {
        start = dates[0];
        $("suggestDate").value = start;
      }
    }
    if (!start) return;

    const byDate = {};
    for (const x of dl) if (x.date) byDate[x.date] = x;

    const filled = ["", "", "", "", "", "", ""];
    let matches = 0;
    for (let i = 0; i < 7; i++) {
      const d = addDaysISO(start, i);
      if (byDate[d]) {
        filled[i] = combineLunch(byDate[d]);
        matches++;
      }
    }
    // Fallback: file had no usable dates → fill in order (Mon..Sun).
    if (matches === 0 && dl.every((x) => !x.date)) {
      for (let i = 0; i < 7 && i < dl.length; i++) {
        filled[i] = combineLunch(dl[i]);
        matches++;
      }
    }

    suggestState.lunches = filled;
    renderLunchInputs();
    $("lunchLoadedInfo").textContent =
      matches > 0 ? tf("lunchFilled", { n: matches, d: start }) : tf("lunchLoaded", { n: dl.length });
  }

  async function requestSuggestion(mode, btn) {
    const weekStart = $("suggestDate").value;
    if (!weekStart) {
      banner(t("requiredDate"), "error");
      return;
    }
    const original = btn.textContent;
    btn.disabled = true;
    btn.textContent = t("generating");
    try {
      const body = {
        mode,
        weekStart,
        constraints: $("constraints").value,
        lang: window.I18N.lang,
        lunches: mode === "dinners" ? suggestState.lunches : [],
      };
      const week = await api("POST", "/api/suggest", body);
      suggestState.week = normalizeWeek(week);
      suggestState.week.weekStart = weekStart;
      renderSuggestResult();
    } catch (e) {
      showError(e);
    } finally {
      btn.disabled = false;
      btn.textContent = original;
    }
  }

  function renderSuggestResult() {
    const container = $("suggestResult");
    container.innerHTML = "";
    if (!suggestState.week) return;
    const editor = el("div", { class: "editor card" });
    renderWeekEditor(editor, suggestState.week, {
      saveLabel: t("saveSuggestion"),
      onSave: saveWeekHandler,
    });
    container.appendChild(editor);
  }

  // ---- ADD tab ----------------------------------------------------------
  const addState = { week: null };

  async function extractFile(btn) {
    const f = $("fileInput").files[0];
    if (!f) return;
    const original = btn.textContent;
    btn.disabled = true;
    btn.textContent = t("extracting");
    try {
      const fd = new FormData();
      fd.append("file", f);
      const week = await api("POST", "/api/menus/extract", fd, true);
      addState.week = normalizeWeek(week);
      if (!addState.week.weekStart) addState.week.weekStart = $("addDate").value;
      renderAddEditor();
    } catch (e) {
      showError(e);
    } finally {
      btn.disabled = false;
      btn.textContent = original;
    }
  }

  function startBlank() {
    addState.week = emptyWeek();
    addState.week.weekStart = $("addDate").value;
    renderAddEditor();
  }

  function renderAddEditor() {
    const container = $("addResult");
    container.innerHTML = "";
    if (!addState.week) return;
    const editor = el("div", { class: "editor card" });
    renderWeekEditor(editor, addState.week, { onSave: saveWeekHandler });
    container.appendChild(editor);
  }

  // Shared save used by add + suggest editors.
  async function saveWeekHandler(week, btn) {
    // Prefer the date picker relevant to the active tab.
    const dateFromAdd = $("addDate").value;
    const dateFromSuggest = $("suggestDate").value;
    week.weekStart = week.weekStart || dateFromAdd || dateFromSuggest;
    if (!week.weekStart) {
      banner(t("requiredDate"), "error");
      return;
    }
    const original = btn.textContent;
    btn.disabled = true;
    try {
      await api("POST", "/api/menus", week);
      banner(t("saved"), "success");
      await loadMenus();
    } catch (e) {
      showError(e);
    } finally {
      btn.disabled = false;
      btn.textContent = original;
    }
  }

  // ---- BROWSE tab -------------------------------------------------------
  async function loadMenus() {
    let menus;
    try {
      menus = await api("GET", "/api/menus");
    } catch (e) {
      showError(e);
      return;
    }
    const list = $("menuList");
    list.innerHTML = "";
    if (!menus || menus.length === 0) {
      list.appendChild(el("p", { class: "hint", text: t("browseEmpty") }));
      return;
    }
    for (const m of menus) {
      const card = el("div", { class: "card menu-card" });
      const heading = m.title ? m.title + " · " + m.weekStart : m.weekStart;
      card.appendChild(el("h3", { text: heading }));

      const editorHost = el("div", { class: "hidden editor" });
      const week = normalizeWeek(m);

      const viewBtn = el("button", { class: "secondary", text: t("viewBtn") });
      viewBtn.addEventListener("click", () => {
        if (editorHost.classList.contains("hidden")) {
          renderWeekEditor(editorHost, week, { onSave: saveWeekHandler });
          editorHost.classList.remove("hidden");
        } else {
          editorHost.classList.add("hidden");
        }
      });

      const delBtn = el("button", { class: "danger", text: t("deleteBtn") });
      delBtn.addEventListener("click", async () => {
        if (!confirm(t("confirmDelete"))) return;
        try {
          await api("DELETE", "/api/menus/" + m.id);
          await loadMenus();
        } catch (e) {
          showError(e);
        }
      });

      card.appendChild(el("div", { class: "actions" }, [viewBtn, delBtn]));
      card.appendChild(editorHost);
      list.appendChild(card);
    }
  }

  // ---- tabs + i18n ------------------------------------------------------
  function switchTab(name) {
    document.querySelectorAll(".tab").forEach((b) =>
      b.classList.toggle("active", b.dataset.tab === name)
    );
    document.querySelectorAll(".panel").forEach((p) =>
      p.classList.toggle("hidden", p.id !== "tab-" + name)
    );
    if (name === "browse") loadMenus();
  }

  function applyI18n() {
    document.documentElement.lang = window.I18N.lang;
    document.title = t("appTitle");
    const map = {
      appTitle: "appTitle",
      tagline: "tagline",
      langLabel: "langLabel",
      navSuggest: "navSuggest",
      navAdd: "navAdd",
      navBrowse: "navBrowse",
      suggestHeading: "suggestHeading",
      suggestHint: "suggestHint",
      "lbl-targetDate": "targetDate",
      "lbl-constraints": "constraintsLabel",
      "lbl-lunchUpload": "lunchUpload",
      btnExtractLunches: "extractLunchesBtn",
      lunchUploadHint: "lunchUploadHint",
      "lbl-lunches": "lunchesLabel",
      btnProposeDinners: "proposeDinners",
      btnGenerateWeek: "generateWeek",
      addHeading: "addHeading",
      "lbl-weekStart": "weekStart",
      "lbl-upload": "uploadLabel",
      extractHint: "extractHint",
      btnExtract: "extractBtn",
      btnStartBlank: "startBlank",
      browseHeading: "browseHeading",
    };
    for (const [id, key] of Object.entries(map)) {
      const node = $(id);
      if (node) node.textContent = t(key);
    }
    $("constraints").placeholder = t("constraintsPlaceholder");
    $("langSelect").value = window.I18N.lang;

    // Re-render language-dependent dynamic content.
    renderLunchInputs();
    if (suggestState.week) renderSuggestResult();
    if (addState.week) renderAddEditor();
  }

  // ---- init -------------------------------------------------------------
  async function init() {
    // Adopt server default language on first visit (no stored preference).
    if (!localStorage.getItem("lang")) {
      try {
        const cfg = await api("GET", "/api/config");
        if (cfg && cfg.defaultLang) window.I18N.setLang(cfg.defaultLang);
      } catch (_) {}
    }

    $("langSelect").addEventListener("change", (e) => {
      window.I18N.setLang(e.target.value);
      applyI18n();
    });
    document.querySelectorAll(".tab").forEach((b) =>
      b.addEventListener("click", () => switchTab(b.dataset.tab))
    );
    $("btnProposeDinners").addEventListener("click", (e) => requestSuggestion("dinners", e.target));
    $("btnGenerateWeek").addEventListener("click", (e) => requestSuggestion("week", e.target));
    $("btnExtractLunches").addEventListener("click", (e) => extractLunchesFile(e.target));
    $("suggestDate").addEventListener("change", () => {
      if (suggestState.datedLunches.length) fillLunchesFromDate();
    });
    $("btnExtract").addEventListener("click", (e) => extractFile(e.target));
    $("btnStartBlank").addEventListener("click", startBlank);

    applyI18n();
    switchTab("suggest");
  }

  document.addEventListener("DOMContentLoaded", init);
})();
