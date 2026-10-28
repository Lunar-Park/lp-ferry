/*
 * Ferry's Ingest view: the cards ferry has planned from, the plans, the transfers
 * running them and the receipt each transfer leaves.
 *
 * mount(el, api) is the whole contract. Data comes from ferry's read ops and every
 * refresh is triggered by the ferry events park streams, so the view never polls.
 *
 * The window-wide sidebar carries the two things a person chooses between: the card
 * (a source folder) filtering the plans, and the destinations the plans land in. A
 * plan opened from either is shown with every file in it, one table row each.
 */

const STYLE_ID = "ferry-ingest-style";

const CSS = `
.ferry { display: grid; gap: var(--space-4); padding: var(--content-pad);
  height: 100%; overflow-y: auto; align-content: start;
  max-width: var(--content-max); margin: 0 auto; }
.ferry__head { display: flex; align-items: baseline; gap: var(--space-3);
  flex-wrap: wrap; }
.ferry__head h2 { font-size: var(--text-lg); }
.ferry__head .btn { margin-left: auto; }
.ferry__counts { color: var(--ink-3); font-size: var(--text-sm); }
.ferry__section { padding: var(--space-3) var(--space-4); display: grid;
  gap: var(--space-2); }
.ferry__section-head { display: flex; align-items: center; gap: var(--space-2); }
.ferry__section-head .segmented { margin-left: auto; }
/* A queue reads across, so a narrow window scrolls the table rather than
   folding every value onto a second line. */
.ferry__scroll { overflow-x: auto; }
.ferry__scroll .table td, .ferry__scroll .table th { white-space: nowrap; }
.ferry__scroll .ferry__file { max-width: 30ch; }
.ferry__now { display: grid; gap: var(--space-3); }
.ferry__now-grid { display: grid; gap: var(--space-3);
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
.ferry__pick { display: grid; gap: var(--space-1); }
.ferry__pick-label { color: var(--ink-3); font-size: var(--text-xs); }
.ferry__pick-value { font-family: var(--font-mono); font-size: var(--text-sm);
  color: var(--ink-1); overflow: hidden; text-overflow: ellipsis; }
.ferry__bar { width: 120px; }
.ferry__bar .meter__track { height: 4px; }
.ferry__row { cursor: pointer; }
.ferry__row[aria-current='true'] td { background: var(--sel-soft); }
.ferry__file { max-width: 42ch; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; }
.ferry__quiet { color: var(--ink-3); font-size: var(--text-sm); }
.ferry__faults { margin: 0; padding-left: var(--space-4); color: var(--bad);
  font-family: var(--font-mono); font-size: var(--text-xs); }
`;

/* ---- builders, so the view carries nothing from park's app ---- */

const NS = "http://www.w3.org/2000/svg";

function h(tag, props, ...kids) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === null || value === undefined || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "style") Object.assign(node.style, value);
    else if (key === "dataset") {
      if (!node.dataset) node.dataset = {};
      Object.assign(node.dataset, value);
    }
    else if (key.startsWith("on") && typeof value === "function") {
      node.addEventListener(key.slice(2).toLowerCase(), value);
    } else if (key in node && key !== "list" && typeof value !== "object") node[key] = value;
    else node.setAttribute(key, value === true ? "" : String(value));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    node.append(typeof kid === "string" || typeof kid === "number" ? document.createTextNode(String(kid)) : kid);
  }
  return node;
}

function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

/* A status is a shape and a word. Where there is no SVG (a stub, a text
   reader), the word still carries the state. */
function svg(tag, attrs) {
  if (typeof document.createElementNS !== "function") return null;
  const node = document.createElementNS(NS, tag);
  for (const [key, value] of Object.entries(attrs || {})) node.setAttribute(key, String(value));
  return node;
}

function glyph(tone, size = 8) {
  const box = svg("svg", { width: size, height: size, viewBox: "0 0 8 8", "aria-hidden": "true" });
  if (!box) return null;
  const fill = "currentColor";
  if (tone === "ok") box.append(svg("circle", { cx: 4, cy: 4, r: 3.1, fill }));
  else if (tone === "warn") box.append(svg("path", { d: "M4 0.9L7.3 6.9H0.7z", fill }));
  else if (tone === "bad") box.append(svg("path", { d: "M2.2 0.9h3.6l1.8 3.1-1.8 3.1H2.2L0.4 4z", fill }));
  else if (tone === "run") box.append(svg("path", { d: "M1.4 0.8l5.6 3.2-5.6 3.2z", fill }));
  else if (tone === "idle") box.append(svg("rect", { x: 1.1, y: 1.1, width: 5.8, height: 5.8, fill: "none", stroke: "currentColor", "stroke-width": 1.3 }));
  else box.append(svg("circle", { cx: 4, cy: 4, r: 3.1, fill: "none", stroke: "currentColor", "stroke-width": 1.3 }));
  return box;
}

function status(tone, word, title) {
  return h("span", { class: `status status--${tone}`, title: title || null },
    glyph(tone, 8), h("span", { text: word }));
}

const ICONS = {
  refresh: "M20 12a8 8 0 10-3 6.2M20 6v5h-5",
  ingest: "M12 4v9M8.5 9.5L12 13l3.5-3.5M5 15v4h14v-4",
  source: "M12 4a8 8 0 100 16 8 8 0 000-16zM4 12h16M12 4c2.5 2.4 3.8 5.1 3.8 8s-1.3 5.6-3.8 8c-2.5-2.4-3.8-5.1-3.8-8S9.5 6.4 12 4z",
  folder: "M4 6h6l2 2h8v10H4z",
};

function icon(name, size = 14) {
  const node = svg("svg", {
    width: size, height: size, viewBox: "0 0 24 24", fill: "none",
    stroke: "currentColor", "stroke-width": 1.6, "stroke-linecap": "round",
    "stroke-linejoin": "round", "aria-hidden": "true",
  });
  if (!node) return null;
  node.append(svg("path", { d: ICONS[name] || ICONS.folder }));
  return node;
}

function button(label, options = {}) {
  const node = h("button", {
    class: `btn${options.small ? " btn--sm" : ""}${options.quiet ? " btn--quiet" : ""}`,
    type: "button",
  }, options.icon ? icon(options.icon, 13) : null, h("span", { text: label }));
  if (options.onClick) node.addEventListener("click", options.onClick);
  return node;
}


/* A read that has not answered yet says so, rather than showing an empty view. */
function busyLine(label) {
  return h("div", { class: "busy", role: "status", "aria-live": "polite" },
    h("div", { class: "busy__ring" }),
    h("span", { class: "quiet", text: label }));
}

function installStyles() {
  if (document.getElementById(STYLE_ID)) return;
  const style = h("style", { id: STYLE_ID });
  style.textContent = CSS;
  document.head.append(style);
}

/* ---- machine values ---- */

function bytes(count) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = Number(count) || 0;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

function ago(stamp) {
  const at = Date.parse(stamp);
  if (Number.isNaN(at)) return stamp ? String(stamp) : "";
  const delta = Math.max(0, (Date.now() - at) / 1000);
  if (delta < 60) return "just now";
  if (delta < 3600) return `${Math.floor(delta / 60)}m ago`;
  if (delta < 86400) return `${Math.floor(delta / 3600)}h ago`;
  return `${Math.floor(delta / 86400)}d ago`;
}

function short(value, limit = 10) {
  return String(value || "").slice(0, limit);
}

/* A transfer moves queued, running, then copied, partial or failed. */
const TRANSFER_TONE = {
  queued: "idle",
  running: "run",
  copied: "ok",
  partial: "warn",
  failed: "bad",
};

const RECEIPT_TONE = { ok: "ok", partial: "warn", failed: "bad" };

/* ---- the view ---- */

export async function mount(el, api) {
  return start(el, api);
}

async function start(el, api) {
  installStyles();
  el.classList.add("ferry");
  el.textContent = "";

  const route = api.route ? api.route() : { params: {} };
  const params = route.params || {};
  const state = {
    sources: [],
    plans: [],
    transfers: [],
    receipts: [],
    detail: null,
    source: params.source || "",
    destination: params.destination || "",
    plan: params.plan || "",
    filter: "all",
    error: "",
    loading: true,
  };

  const head = h("div", { class: "ferry__head" });
  const notice = h("div");
  const emptySlot = h("div");
  const now = h("section", { class: "card ferry__section", dataset: { name: "now" } });
  const sourcesBox = h("section", { class: "card ferry__section" });
  const plansBox = h("section", { class: "card ferry__section" });
  const transfersBox = h("section", { class: "card ferry__section" });
  const receiptsBox = h("section", { class: "card ferry__section" });
  const busySlot = busyLine("Reading the cards and the plans");
  el.append(head, notice, busySlot, emptySlot, now, sourcesBox, plansBox, transfersBox, receiptsBox);

  /* ---- reading ---- */

  async function load() {
    try {
      const [sources, plans, transfers, receipts] = await Promise.all([
        api.call("ferry.source.list", {}),
        api.call("ferry.plan.list", state.source ? { source: state.source, limit: 25 } : { limit: 25 }),
        api.call("ferry.transfer.list", { limit: 25 }),
        api.call("ferry.receipt.list", { limit: 25 }),
      ]);
      state.sources = sources || [];
      state.plans = plans || [];
      state.transfers = transfers || [];
      state.receipts = receipts || [];
      state.error = "";
      if (state.plan) await openPlan(state.plan, false);
      else if (state.plans.length && state.destination) {
        const match = state.plans.find((plan) => plan.destination === state.destination);
        if (match) await openPlan(match.id, false);
      }
    } catch (problem) {
      state.error = problem && problem.message ? problem.message : String(problem);
    }
    state.loading = false;
    draw();
  }

  async function openPlan(id, redraw = true) {
    try {
      state.detail = await api.call("ferry.plan.get", { id });
      state.plan = id;
    } catch (problem) {
      state.error = problem && problem.message ? problem.message : String(problem);
    }
    if (redraw) draw();
  }

  /* ---- drawing ---- */

  function untouched() {
    return !state.loading && !state.error && !state.sources.length && !state.plans.length
      && !state.transfers.length && !state.receipts.length;
  }

  function draw() {
    busySlot.hidden = true;
    drawHead();
    clear(emptySlot);
    if (untouched()) return drawNothing();
    for (const box of [now, sourcesBox, plansBox, transfersBox, receiptsBox]) box.hidden = false;
    drawNow();
    drawSources();
    drawPlans();
    drawTransfers();
    drawReceipts();
    drawSidebar();
  }

  function drawHead() {
    clear(head);
    const counts = state.loading
      ? "reading"
      : `${state.sources.length} cards, ${state.plans.length} plans, ${state.transfers.length} transfers, ${state.receipts.length} receipts`;
    const refresh = button("Read again", { small: true, quiet: true, icon: "refresh", onClick: () => load() });
    head.append(
      h("span", { class: "ferry__counts", text: counts }),
      h("span", { style: { flex: "1" } }),
      refresh);
    clear(notice);
    if (state.error) {
      notice.append(h("div", { class: "notice notice--bad", role: "alert" },
        glyph("bad", 8), h("span", { text: state.error })));
    }
  }

  /* The plan in hand: where it comes from, where it lands and what it holds. */
  function drawNothing() {
    for (const box of [now, sourcesBox, plansBox, transfersBox, receiptsBox]) {
      box.hidden = true;
      clear(box);
    }
    emptySlot.append(h("div", { class: "empty" },
      icon("ingest", 30),
      h("h2", { text: "Nothing has been planned yet" }),
      h("p", { text: "A plan is one card's files, where they land and what is skipped. The first plan fills this view." }),
      button("Read again", { onClick: () => load() })));
    drawSidebar();
  }

  function drawNow() {
    clear(now);
    const plan = state.detail;
    now.append(h("div", { class: "ferry__section-head" },
      h("span", { class: "eyebrow", text: "In hand" }),
      plan ? status("ok", plan.label || short(plan.id, 8)) : status("idle", "no plan chosen")));
    if (!plan) {
      now.append(h("p", { class: "quiet", style: { fontSize: "var(--text-sm)" },
        text: "Pick a plan below, or a card or destination in the sidebar, to see every file in it." }));
      return;
    }
    now.append(h("div", { class: "ferry__now-grid" },
      pick("Card", plan.source),
      pick("Lands in", plan.destination),
      pick("Files", `${plan.files} files, ${plan.copies} copies, ${plan.skips} skipped`),
      pick("Size", bytes(plan.bytes)),
      pick("Planned", `${ago(plan.created)} · fingerprint ${short(plan.fingerprint, 12)}`),
      pick("Conflict rule", plan.on_conflict || "ask")));
    const items = plan.items || [];
    if (!items.length) {
      now.append(h("p", { class: "quiet", style: { fontSize: "var(--text-sm)" },
        text: "The plan holds no files." }));
      return;
    }
    now.append(table(
      ["File", "Lands at", "Kind", "Size", "Action"],
      items.slice(0, 200).map((item) => h("tr", {},
        h("td", { class: "mono ferry__file", title: item.rel, text: item.rel }),
        h("td", { class: "mono ferry__file", title: item.dest, text: item.dest }),
        h("td", { text: item.kind || "-" }),
        h("td", { class: "num", text: bytes(item.size) }),
        h("td", {}, actionStatus(item.action))))));
  }

  function pick(label, value) {
    return h("div", { class: "ferry__pick" },
      h("span", { class: "ferry__pick-label", text: label }),
      h("span", { class: "ferry__pick-value", title: value, text: value }));
  }

  function actionStatus(action) {
    if (action === "copy") return status("ok", "copy");
    if (action === "skip") return status("idle", "skip");
    return status("info", action || "copy");
  }

  function drawSources() {
    clear(sourcesBox);
    sourcesBox.append(h("div", { class: "ferry__section-head" },
      h("span", { class: "eyebrow", text: "Cards" }),
      h("span", { class: "section__count num", text: String(state.sources.length) })));
    if (!state.sources.length) {
      sourcesBox.append(emptyLine("No card has been planned from yet.",
        "Plan a source folder and the card appears here, with what it holds."));
      return;
    }
    sourcesBox.append(h("div", { class: "ferry__scroll" }, table(
      ["Card", "Plans", "Files", "Size", "Last plan", "Here"],
      state.sources.map((source) => h("tr", { class: "ferry__row", onClick: () => selectSource(source.source) },
        h("td", { class: "mono ferry__file", title: source.source, text: source.source }),
        h("td", { class: "num", text: String(source.plans) }),
        h("td", { class: "num", text: String(source.files) }),
        h("td", { class: "num", text: bytes(source.bytes) }),
        h("td", { text: ago(source.last_plan) }),
        h("td", {}, source.mounted ? status("ok", "mounted") : status("warn", "not here"))))
    )));
  }

  function drawPlans() {
    clear(plansBox);
    plansBox.append(h("div", { class: "ferry__section-head" },
      h("span", { class: "eyebrow", text: "Plans" }),
      h("span", { class: "section__count num", text: String(state.plans.length) })));
    if (!state.plans.length) {
      plansBox.append(emptyLine(
        state.source ? "No plan from this card yet." : "No plan yet.",
        "A plan is what a card is about to become: the files, where they land and what is skipped."));
      return;
    }
    plansBox.append(h("div", { class: "ferry__scroll" }, table(
      ["Label", "Card", "Files", "Size", "Planned", "Fingerprint"],
      state.plans.map((plan) => h("tr", {
        class: "ferry__row",
        "aria-current": plan.id === state.plan ? "true" : null,
        onClick: () => openPlan(plan.id),
      },
        h("td", { text: plan.label }),
        h("td", { class: "mono ferry__file", title: plan.source, text: plan.source }),
        h("td", { class: "num", text: `${plan.copies} / ${plan.files}` }),
        h("td", { class: "num", text: bytes(plan.bytes) }),
        h("td", { text: ago(plan.created) }),
        h("td", { class: "mono", text: short(plan.fingerprint, 10) })))
    )));
  }

  function filterTransfers() {
    const open = (transfer) => transfer.status === "queued" || transfer.status === "running";
    if (state.filter === "open") return state.transfers.filter(open);
    if (state.filter === "done") return state.transfers.filter((t) => t.status === "copied");
    if (state.filter === "failed") {
      return state.transfers.filter((t) => t.status === "failed" || t.status === "partial");
    }
    return state.transfers;
  }

  function drawTransfers() {
    clear(transfersBox);
    const options = [
      ["all", "All"],
      ["open", "Open"],
      ["done", "Done"],
      ["failed", "Trouble"],
    ];
    const pills = h("div", { class: "segmented", role: "group", "aria-label": "Transfers to show" },
      options.map(([value, label]) => {
        const pill = h("button", {
          class: "segmented__option",
          type: "button",
          "aria-pressed": state.filter === value ? "true" : "false",
          text: label,
        });
        pill.addEventListener("click", () => {
          state.filter = value;
          drawTransfers();
        });
        return pill;
      }));
    transfersBox.append(h("div", { class: "ferry__section-head" },
      h("span", { class: "eyebrow", text: "Transfers" }),
      h("span", { class: "section__count num", text: String(state.transfers.length) }),
      pills));
    const rows = filterTransfers();
    if (!state.transfers.length) {
      transfersBox.append(emptyLine("No transfer has been queued yet.",
        "Queuing a plan starts a copy job; its progress and its receipt show here."));
      return;
    }
    if (!rows.length) {
      transfersBox.append(emptyLine("Nothing in that state.", "Choose another filter."));
      return;
    }
    transfersBox.append(h("div", { class: "ferry__scroll" }, table(
      ["Transfer", "Plan", "State", "Progress", "Files", "Size", "Ended"],
      rows.map((transfer) => {
        const tone = TRANSFER_TONE[transfer.status] || "idle";
        const row = h("tr", {},
          h("td", { class: "mono", text: short(transfer.id) }),
          h("td", { class: "mono", text: short(transfer.plan) }),
          h("td", {}, status(tone, transfer.status || "queued", transfer.note || null)),
          h("td", {}, meter(transfer.fraction, tone)),
          h("td", { class: "num", text: `${transfer.files} (${transfer.failed} failed)` }),
          h("td", { class: "num", text: bytes(transfer.bytes) }),
          h("td", { text: transfer.ended ? ago(transfer.ended) : "-" }));
        if ((transfer.errors || []).length) {
          row.dataset.faults = "true";
        }
        return row;
      }))));
    const faulty = rows.filter((transfer) => (transfer.errors || []).length);
    for (const transfer of faulty) {
      transfersBox.append(h("ul", { class: "ferry__faults" },
        transfer.errors.slice(0, 4).map((line) => h("li", { text: `${short(transfer.id)}: ${line}` }))));
    }
  }

  function meter(fraction, tone) {
    const width = Math.max(0, Math.min(1, Number(fraction) || 0)) * 100;
    const fillClass = tone === "ok" ? "meter__fill meter__fill--ok"
      : tone === "warn" ? "meter__fill meter__fill--warn"
        : tone === "bad" ? "meter__fill meter__fill--bad" : "meter__fill";
    const label = Number.isFinite(Number(fraction)) ? `${Math.round(width)}%` : "unknown";
    return h("div", { class: "ferry__bar", title: label },
      h("div", { class: "meter" },
        h("div", { class: "meter__track" },
          h("div", { class: fillClass, style: { width: `${width}%` } }))));
  }

  function drawReceipts() {
    clear(receiptsBox);
    receiptsBox.append(h("div", { class: "ferry__section-head" },
      h("span", { class: "eyebrow", text: "Receipts" }),
      h("span", { class: "section__count num", text: String(state.receipts.length) })));
    if (!state.receipts.length) {
      receiptsBox.append(emptyLine("No receipt yet.",
        "A finished transfer writes one: every file, its hash and anything that failed."));
      return;
    }
    receiptsBox.append(h("div", { class: "ferry__scroll" }, table(
      ["Receipt", "Kind", "State", "Files", "Size", "Digest", "Written"],
      state.receipts.map((receipt) => h("tr", {},
        h("td", { class: "mono", text: short(receipt.id) }),
        h("td", { text: receipt.kind }),
        h("td", {}, status(RECEIPT_TONE[receipt.status] || "idle", receipt.status || "ok")),
        h("td", { class: "num", text: String(receipt.files) }),
        h("td", { class: "num", text: bytes(receipt.bytes) }),
        h("td", { class: "mono", text: short(receipt.digest) }),
        h("td", { text: ago(receipt.created) })))
    )));
  }

  function drawSidebar() {
    if (!api.sidebar) return;
    const destinations = [];
    const seen = new Set();
    for (const plan of state.plans) {
      if (!plan.destination || seen.has(plan.destination)) continue;
      seen.add(plan.destination);
      destinations.push({
        label: plan.destination.split("/").pop() || plan.destination,
        note: String(plan.copies || plan.files || ""),
        icon: "folder",
        active: state.destination === plan.destination,
        onSelect: () => {
          state.destination = plan.destination;
          state.source = "";
          choose(plan.id, { destination: plan.destination });
        },
      });
    }
    const cards = state.sources.map((source) => ({
      label: source.label || source.source,
      note: String(source.files),
      icon: "source",
      glyph: source.mounted ? "ok" : "warn",
      active: state.source === source.source,
      onSelect: () => selectSource(source.source),
    }));
    const sections = [];
    if (cards.length) sections.push({ kind: "list", title: "Cards", items: cards });
    if (destinations.length) sections.push({ kind: "list", title: "Destinations", items: destinations });
    api.sidebar({ title: "Ingest", sections });
  }

  /* Choosing a card filters the plans to it; choosing a destination opens the
     newest plan landing there. Both are route params, so a link can say either. */
  function selectSource(source) {
    const next = state.source === source ? "" : source;
    state.source = next;
    state.destination = "";
    if (api.go) api.go("ingest", { source: next || null, plan: null, destination: null });
    else load();
  }

  function choose(planId, params) {
    if (api.go) api.go("ingest", { plan: planId, ...params });
    else openPlan(planId);
  }

  /* ---- pieces ---- */

  function table(headers, rows, ...rest) {
    const view = h("table", { class: "table" });
    view.append(h("thead", {}, h("tr", {}, headers.map((label, index) =>
      h("th", { class: index > 0 && /files|size|plans|written|ended|progress/i.test(label) ? "num" : null, text: label })))));
    view.append(h("tbody", {}, rows));
    return view;
  }

  function emptyLine(title, body) {
    return h("p", { class: "ferry__quiet" },
      h("span", { text: title }),
      body ? h("span", { class: "faint", text: ` ${body}` }) : null);
  }

  /* ---- live ---- */

  let unsubscribe = null;
  if (typeof api.subscribe === "function") {
    unsubscribe = api.subscribe(
      ["ferry.plan.created", "ferry.transfer.*", "ferry.receipt.recorded"],
      () => load(),
    );
  }
  await load();

  return function unmount() {
    if (typeof unsubscribe === "function") unsubscribe();
    el.textContent = "";
    el.classList.remove("ferry");
    if (api.toolbar) api.toolbar(null);
    if (api.sidebar) api.sidebar(null);
  };
}
