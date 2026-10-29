/*
 * Ferry's Ingest view: every card that has come in, as one dense list.
 *
 * A row is one offload: the card, where it lands, how much, and where it stands (planned,
 * queued, copying, copied, partial, failed), with its transfer and receipt already joined
 * by ferry.ingest.list, so the page is drawn by one read. Rows are grouped by what they
 * need from D: in progress, needs a look, planned, done. A proxy or Resolve receipt is a
 * row of its own. The sidebar lists the cards and narrows the list to one; a row opens in
 * the inspector, which holds the paths, the copy's figures, the problems and the files.
 *
 * The page's one action is "Plan a card": a folder and where it lands. A planned offload
 * is queued, and a failed one retried, from its inspector.
 *
 * Events patch the rows they name (progress costs no request); only a new plan or a new
 * transfer reads again. Keys: arrows or j and k walk the list, Enter opens, Escape closes.
 */

const STYLE_ID = "ferry-ingest-style";
const FILE_PAGE = 200;

const CSS = `
.ingest { display: grid; grid-template-columns: minmax(0, 1fr); height: 100%; min-height: 0; }
.ingest--open { grid-template-columns: minmax(0, 1fr) clamp(300px, 30%, var(--shell-inspect)); }
.ingest__main { display: grid; grid-template-rows: auto minmax(0, 1fr); gap: var(--space-2);
  min-width: 0; min-height: 0; padding: var(--space-2) var(--space-4) 0; }
.ingest__top { display: grid; gap: var(--space-2); }
.ingest__list { min-height: 0; overflow-y: auto; padding-bottom: var(--space-5); }
.ingest__list > * { max-width: 960px; }
.ingest__group { display: grid; gap: var(--row-gap); margin-bottom: var(--block-gap); }
.ingest__group-head { display: flex; align-items: baseline; gap: var(--space-2); padding: 0 var(--space-2); }
.ingest__group-count { color: var(--ink-4); font-size: var(--text-xs); font-variant-numeric: tabular-nums; }

/* a row is the shared list row, with a state word and a meter at its end */
.ingest__row { cursor: pointer; }
.ingest__row[aria-current='true'] { background: var(--sel-soft); box-shadow: inset 0 0 0 1px var(--sel-line); color: var(--ink-1); }
.ingest__row:focus-visible { outline: 1px solid var(--focus); outline-offset: -1px; }
.ingest__row .list-row__avatar { background: none; }
.ingest__row[data-tone='run'] .list-row__avatar { color: var(--run); }
.ingest__row[data-tone='ok'] .list-row__avatar { color: var(--ok); }
.ingest__row[data-tone='warn'] .list-row__avatar { color: var(--warn); }
.ingest__row[data-tone='bad'] .list-row__avatar { color: var(--bad); }
.ingest__row[data-tone='idle'] .list-row__avatar { color: var(--idle); }
.ingest__word { font-size: var(--text-xs); white-space: nowrap; }
.ingest__word[data-tone='run'] { color: var(--run); }
.ingest__word[data-tone='warn'] { color: var(--warn); }
.ingest__word[data-tone='bad'] { color: var(--bad); }
.stat-line__item--run .stat-line__value { color: var(--run); }
.stat-line__item--bad .stat-line__value { color: var(--bad); }
.ingest__bar { width: 56px; }
.ingest__bar .meter__track { height: 3px; }
.ingest__kind { color: var(--ink-4); font-size: var(--text-2xs); text-transform: uppercase;
  letter-spacing: var(--tracking-eyebrow); }

/* planning a card */
.ingest__plan { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto auto; gap: var(--space-2);
  align-items: end; padding: var(--space-3); border: 1px solid var(--rule-2); border-radius: var(--r-3);
  background: var(--well); }
.ingest__plan .field { min-width: 0; }
.ingest__plan-note { grid-column: 1 / -1; }
.ingest__note { display: flex; align-items: center; gap: var(--space-2); min-height: var(--row-h);
  padding: 0 var(--space-3); border: 1px solid var(--rule-2); border-radius: var(--r-2); background: var(--well);
  color: var(--ink-2); font-size: var(--text-xs); }
.ingest__note--bad { border-color: var(--bad-line); background: var(--bad-soft); color: var(--ink-1); }
.ingest__note svg { flex: none; }
.ingest__note--bad svg { color: var(--bad); }

/* the inspector */
.ingest__panel { min-width: 0; min-height: 0; display: flex; }
.ingest__panel .panel { width: 100%; }
.ingest__pairs { display: grid; gap: var(--space-2); min-width: 0; }
.ingest__pair { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3);
  min-width: 0; font-size: var(--text-xs); }
.ingest__pair-label { flex: none; color: var(--ink-4); }
.ingest__pair-value { min-width: 0; overflow: hidden; color: var(--ink-2); font-size: var(--text-xs);
  text-align: right; text-overflow: ellipsis; white-space: nowrap; }
.ingest__pair-value[data-mono='true'] { font-family: var(--font-mono); font-size: var(--text-2xs);
  direction: rtl; }
.ingest__pair .btn { flex: none; margin-right: calc(var(--space-2) * -1); }
.ingest__issues { display: grid; gap: var(--row-gap); margin: 0; padding: 0; list-style: none; }
.ingest__issues li { display: flex; gap: var(--space-2); padding: var(--space-1) var(--space-2);
  border-radius: var(--r-1); background: var(--bad-soft); color: var(--ink-1); font-size: var(--text-xs); }
.ingest__issues svg { flex: none; margin-top: 3px; color: var(--bad); }
.ingest__files { display: grid; gap: 1px; }
.ingest__file { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--space-2); align-items: baseline;
  min-height: 22px; font-size: var(--text-2xs); color: var(--ink-3); }
.ingest__file-name { overflow: hidden; color: var(--ink-2); font-family: var(--font-mono);
  text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; }
.ingest__file[data-skip='true'] .ingest__file-name { color: var(--ink-4); text-decoration: line-through; }
.ingest__more { color: var(--ink-4); font-size: var(--text-xs); padding-top: var(--space-1); }

@media (max-width: 1080px) {
  .ingest--open { grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr) minmax(0, 55%); }
  .ingest__panel { border-top: 1px solid var(--rule-1); }
  .ingest__plan { grid-template-columns: minmax(0, 1fr) auto auto; }
}
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
    else if (key === "dataset") Object.assign(node.dataset, value);
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

function svg(tag, attrs) {
  if (typeof document.createElementNS !== "function") return null;
  const node = document.createElementNS(NS, tag);
  for (const [key, value] of Object.entries(attrs || {})) node.setAttribute(key, String(value));
  return node;
}

/* One shape per tone, so a state survives greyscale: colour is the second signal. */
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

const ICONS = {
  ingest: "M12 4v9M8.5 9.5L12 13l3.5-3.5M5 15v4h14v-4",
  plus: "M12 5v14M5 12h14",
};

function icon(name, size = 14) {
  const node = svg("svg", {
    width: size, height: size, viewBox: "0 0 24 24", fill: "none",
    stroke: "currentColor", "stroke-width": 1.6, "stroke-linecap": "round",
    "stroke-linejoin": "round", "aria-hidden": "true",
  });
  if (!node) return null;
  node.append(svg("path", { d: ICONS[name] || ICONS.ingest }));
  return node;
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
  return `${unit === 0 ? value : value.toFixed(value >= 100 ? 0 : 1)} ${units[unit]}`;
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

function plural(count, one, many) {
  return count === 1 ? one : many || `${one}s`;
}

function message(error) {
  return error && error.message ? error.message : String(error);
}

/* What each state is called, how it reads at a glance, and the group it sits in. */
const STATES = {
  planned: { tone: "idle", word: "planned", group: "planned" },
  queued: { tone: "idle", word: "queued", group: "now" },
  running: { tone: "run", word: "copying", group: "now" },
  done: { tone: "ok", word: "copied", group: "done" },
  partial: { tone: "warn", word: "partial", group: "look" },
  failed: { tone: "bad", word: "failed", group: "look" },
};

const GROUPS = [
  ["now", "In progress"],
  ["look", "Needs a look"],
  ["planned", "Planned"],
  ["done", "Done"],
];

/* Where an offload lands, in a few words: the folder it goes into, and its own folder
   only when that is not just the card's name again. */
function lands(row) {
  const parts = String(row.destination || "").split("/").filter(Boolean);
  if (!parts.length) return row.destination_name || "";
  const name = parts[parts.length - 1];
  const parent = parts.length > 1 ? parts[parts.length - 2] : "";
  if (!parent) return name;
  return name === row.label || name === row.card ? parent : `${parent}/${name}`;
}

const TRANSFER_STATE = { queued: "queued", running: "running", copied: "done", partial: "partial", failed: "failed" };

function stateOf(row) {
  return STATES[row.state] || STATES.planned;
}

function stateWord(row) {
  const look = stateOf(row);
  if (row.kind !== "offload" && row.state === "done") return "done";
  if (row.state === "running") {
    const fraction = Number(row.fraction);
    return Number.isFinite(fraction) ? `${Math.round(fraction * 100)}%` : look.word;
  }
  if ((row.state === "partial" || row.state === "failed") && row.failed) return `${row.failed} failed`;
  return look.word;
}

export function mount(el, api, route) {
  installStyles();
  const params = ((route && route.params) || (api.route ? api.route().params : {}) || {});

  const state = {
    sources: [],
    rows: [],
    more: false,
    card: params.source || "",     // narrows the list to one card
    open: params.plan || "",       // the row in the inspector
    files: new Map(),              // plan id -> { items, files } read when its row opened
    planning: false,               // the "plan a card" form is open
    busy: "",
    error: "",
    reading: true,
  };

  const top = h("div", { class: "ingest__top" });
  const list = h("div", { class: "ingest__list", role: "list", "aria-label": "Offloads" });
  const main = h("div", { class: "ingest__main" }, top, list);
  const panel = h("div", { class: "ingest__panel" });
  const root = h("section", { class: "ingest" }, main, panel);
  el.append(root);

  /* ---- reading ---- */

  async function load() {
    try {
      const read = await api.call("ferry.ingest.list", {});
      state.sources = (read && read.sources) || [];
      state.rows = (read && read.rows) || [];
      state.more = Boolean(read && read.more);
      state.error = "";
    } catch (error) {
      state.error = message(error);
    }
    state.reading = false;
    draw();
  }

  /* The files of a plan are read once, when its row is opened, a page of them. */
  async function readFiles(plan) {
    if (!plan || state.files.has(plan)) return;
    state.files.set(plan, { loading: true, items: [], files: 0 });
    try {
      const read = await api.call("ferry.plan.get", { id: plan, items_limit: FILE_PAGE });
      state.files.set(plan, { items: (read && read.items) || [], files: (read && read.files) || 0 });
    } catch (error) {
      state.files.delete(plan);
    }
    if (current() && current().plan === plan) drawPanel();
  }

  function current() {
    return state.rows.find((row) => row.id === state.open) || null;
  }

  /* ---- what the page can do ---- */

  async function planCard(source, destination) {
    if (!source || !destination || state.busy) return;
    state.busy = "plan";
    state.error = "";
    drawTop();
    drawTools();
    try {
      const plan = await api.call("ferry.plan", { source, destination });
      state.planning = false;
      state.busy = "";
      state.open = plan && plan.id ? plan.id : state.open;
      if (api.toast) api.toast(`Planned ${plan && plan.label ? plan.label : "the card"}`, "ok");
      await load();
    } catch (error) {
      state.busy = "";
      state.error = `The card was not planned: ${message(error)}`;
      draw();
    }
  }

  async function offload(row) {
    if (!row || state.busy) return;
    state.busy = "offload";
    drawPanel();
    try {
      await api.call("ferry.offload", { plan: row.plan });
      if (api.toast) api.toast(`${row.label} queued`, "ok");
      state.busy = "";
      await load();
    } catch (error) {
      state.busy = "";
      state.error = `The offload was not queued: ${message(error)}`;
      draw();
    }
  }

  /* ---- the page ---- */

  function visible() {
    return state.rows.filter((row) => !state.card || row.source === state.card);
  }

  function draw() {
    root.classList.toggle("ingest--open", Boolean(current()));
    drawTop();
    drawList();
    drawPanel();
    drawTools();
    drawSidebar();
    if (current()) readFiles(current().plan);
  }

  function drawTop() {
    clear(top);
    if (api.ui && !state.reading && (state.rows.length || state.sources.length)) top.append(api.ui.statLine(stats()));
    if (state.error) {
      top.append(h("div", { class: "ingest__note ingest__note--bad", role: "alert" },
        glyph("bad", 8), h("span", { text: state.error }),
        h("button", { class: "btn btn--sm", type: "button", text: "Try again", style: { marginLeft: "auto" }, onClick: () => load() })));
    }
    if (state.planning) top.append(planForm());
  }

  function stats() {
    const offloads = state.rows.filter((row) => row.kind === "offload");
    const running = state.rows.filter((row) => stateOf(row).group === "now").length;
    const trouble = state.rows.filter((row) => stateOf(row).group === "look").length;
    const total = offloads.reduce((sum, row) => sum + (Number(row.bytes) || 0), 0);
    return [
      { value: state.sources.length, label: plural(state.sources.length, "card") },
      { value: offloads.length, label: plural(offloads.length, "offload") },
      running ? { value: running, label: "in progress", tone: "run" } : null,
      trouble ? { value: trouble, label: plural(trouble, "needs a look", "need a look"), tone: "bad" } : null,
      total ? bytes(total) : null,
    ];
  }

  function planForm() {
    const source = h("input", { class: "input mono", type: "text", placeholder: "/Volumes/CARD_A", "aria-label": "The card's folder" });
    const destination = h("input", { class: "input mono", type: "text", placeholder: "/Volumes/Footage/2026-10-28/CARD_A", "aria-label": "Where the files land" });
    const submit = () => planCard(source.value.trim(), destination.value.trim());
    const keys = (event) => {
      if (event.key === "Enter") submit();
      if (event.key === "Escape") {
        state.planning = false;
        draw();
      }
    };
    source.addEventListener("keydown", keys);
    destination.addEventListener("keydown", keys);
    const form = h("div", { class: "ingest__plan" },
      h("label", { class: "field" }, h("span", { class: "field__label", text: "Card" }), source),
      h("label", { class: "field" }, h("span", { class: "field__label", text: "Lands in" }), destination),
      h("button", { class: "btn btn--sm", type: "button", text: "Cancel", onClick: () => {
        state.planning = false;
        draw();
      } }),
      h("button", {
        class: "btn btn--sm btn--primary", type: "button", text: "Plan",
        "aria-busy": state.busy === "plan" ? "true" : "false",
        onClick: submit,
      }));
    setTimeout(() => source.focus(), 0);
    return form;
  }

  /* The page's one primary action. */
  let toolsKey = "";
  function drawTools() {
    if (!api.toolbar) return;
    const key = `${state.planning}|${state.busy}`;
    if (key === toolsKey) return;
    toolsKey = key;
    if (state.planning) {
      api.toolbar(null);
      return;
    }
    api.toolbar(h("button", {
      class: "btn btn--sm btn--primary",
      type: "button",
      onClick: () => {
        state.planning = true;
        draw();
      },
    }, icon("plus", 13), h("span", { text: "Plan a card" })));
  }

  /* ---- the list ---- */

  const rowNodes = new Map();

  function drawList() {
    const focused = document.activeElement && document.activeElement.dataset && document.activeElement.dataset.row;
    clear(list);
    rowNodes.clear();
    if (state.reading) return;
    const rows = visible();
    if (!rows.length && state.error) return;
    if (!rows.length) {
      const sentence = state.card ? "Nothing has come in from this card." : "Nothing has been planned yet.";
      list.append(api.ui
        ? api.ui.pageEmpty(sentence, null, { icon: "ingest" })
        : h("p", { class: "quiet", text: sentence }));
      return;
    }
    for (const [group, title] of GROUPS) {
      const members = rows.filter((row) => stateOf(row).group === group);
      if (!members.length) continue;
      list.append(h("section", { class: "ingest__group" },
        h("div", { class: "ingest__group-head" },
          h("span", { class: "eyebrow", text: title }),
          h("span", { class: "ingest__group-count", text: String(members.length) })),
        members.map((row) => rowNode(row))));
    }
    if (state.more) {
      list.append(h("p", { class: "ingest__more", text: "Older offloads are not shown." }));
    }
    if (focused && rowNodes.has(focused)) rowNodes.get(focused).focus();
  }

  function rowNode(row) {
    const look = stateOf(row);
    const meta = [
      row.kind === "offload" && row.label !== row.card ? row.card : "",
      row.destination ? `\u2192 ${lands(row)}` : "",
      `${row.kind === "offload" ? row.copies : row.files} ${plural(row.kind === "offload" ? row.copies : row.files, "file")}`,
      row.bytes ? bytes(row.bytes) : "",
      ago(row.when),
    ].filter(Boolean).join(" · ");
    const node = api.ui.listRow({
      avatar: glyph(look.tone, 9),
      title: row.label,
      meta,
      trailing: trailing(row),
      onOpen: () => openRow(row.id),
    });
    node.classList.add("ingest__row");
    node.dataset.row = row.id;
    node.dataset.tone = look.tone;
    node.setAttribute("aria-current", row.id === state.open ? "true" : "false");
    rowNodes.set(row.id, node);
    return node;
  }

  function trailing(row) {
    const look = stateOf(row);
    const kids = [];
    if (row.kind !== "offload") kids.push(h("span", { class: "ingest__kind", text: row.kind }));
    if (row.state === "running") {
      const width = Math.max(0, Math.min(1, Number(row.fraction) || 0)) * 100;
      kids.push(h("div", { class: "ingest__bar meter", "aria-hidden": "true" },
        h("div", { class: "meter__track" }, h("div", { class: "meter__fill", style: { width: `${width}%` } }))));
    }
    kids.push(h("span", { class: "ingest__word", dataset: { tone: look.tone }, text: stateWord(row) }));
    return h("span", { class: "ingest__trail", style: { display: "flex", alignItems: "center", gap: "var(--space-2)" } }, kids);
  }

  /* Progress changes a number and a bar, so it changes those and nothing else. */
  function paintProgress(row) {
    const node = rowNodes.get(row.id);
    if (!node) return false;
    const old = node.querySelector(".list-row__trailing");
    if (!old) return false;
    clear(old);
    old.append(trailing(row));
    return true;
  }

  function openRow(id) {
    state.open = state.open === id ? "" : id;
    draw();
  }

  function closeRow() {
    if (!state.open) return false;
    const id = state.open;
    state.open = "";
    draw();
    if (rowNodes.has(id)) rowNodes.get(id).focus();
    return true;
  }

  /* ---- the sidebar: the cards ---- */

  function drawSidebar() {
    if (!api.sidebar) return;
    const cards = state.sources.map((source) => ({
      label: source.label || source.source,
      note: source.plans > 1 ? String(source.plans) : "",
      glyph: source.mounted ? "ok" : "warn",
      active: state.card === source.source,
      onSelect: () => {
        state.card = state.card === source.source ? "" : source.source;
        draw();
      },
    }));
    api.sidebar({ sections: cards.length ? [{ kind: "list", title: "Cards", items: cards }] : [] });
  }

  /* ---- the inspector ---- */

  function pair(label, value, options = {}) {
    if (value === null || value === undefined || value === "") return null;
    const text = String(value);
    return h("div", { class: "ingest__pair" },
      h("span", { class: "ingest__pair-label", text: label }),
      h("span", { class: "ingest__pair-value", title: text, dataset: options.mono ? { mono: "true" } : null,
        text: options.mono ? `\u200e${text}` : text }),
      options.copy ? h("button", {
        class: "btn btn--sm btn--quiet", type: "button", text: "Copy", "aria-label": `Copy the ${label.toLowerCase()}`,
        onClick: () => copy(text),
      }) : null);
  }

  function copy(text) {
    try {
      Promise.resolve(navigator.clipboard.writeText(text)).then(
        () => api.toast && api.toast("Copied", "ok"),
        () => api.toast && api.toast("The clipboard is not available", "error"));
    } catch {
      if (api.toast) api.toast("The clipboard is not available", "error");
    }
  }

  function pairs(rows) {
    return h("div", { class: "ingest__pairs" }, rows.filter(Boolean));
  }

  function drawPanel() {
    clear(panel);
    const row = current();
    if (!row || !api.ui) return;
    const look = stateOf(row);
    const sections = [];
    if (row.source) {
      sections.push({ title: "Where", body: pairs([
        pair("Card", row.source, { mono: true, copy: true }),
        row.mounted ? null : pair("Here", "not mounted"),
        pair("Lands in", row.destination, { mono: true, copy: true }),
        row.kind === "offload" ? pair("Conflicts", row.on_conflict) : null,
      ]) });
    }
    if (row.kind === "offload") {
      sections.push({ title: "Copy", body: pairs([
        pair("Files", `${row.copies} to copy${row.skips ? `, ${row.skips} skipped` : ""}`),
        pair("Size", bytes(row.bytes)),
        row.transfer ? pair("Done", `${row.done} of ${row.total}`) : null,
        row.verified ? pair("Verified", row.verified) : null,
        pair("Machine", row.machine),
        pair("Started", row.started ? ago(row.started) : ""),
        pair("Ended", row.ended ? ago(row.ended) : ""),
        row.attempts > 1 ? pair("Attempts", row.attempts) : null,
        pair("Receipt", row.digest ? row.digest.slice(0, 16) : "", { mono: true }),
      ]) });
    } else {
      sections.push({ title: "Result", body: pairs([
        pair("Files", row.files), pair("Size", bytes(row.bytes)), pair("Machine", row.machine),
        pair("Written", ago(row.ended)),
      ]) });
    }
    if ((row.errors || []).length) {
      sections.push({ title: "Problems", body: h("ul", { class: "ingest__issues" },
        row.errors.slice(0, 20).map((line) => h("li", {}, glyph("bad", 8), h("span", { text: line })))) });
    }
    if (row.kind === "offload") sections.push({ title: "Files", body: filesBody(row) });
    panel.append(api.ui.inspectorPanel({
      title: row.label,
      subtitle: `${stateWord(row)} · ${ago(row.when)}`,
      sections,
      actions: actions(row),
      label: `${row.label} details`,
    }));
  }

  function actions(row) {
    if (row.kind !== "offload") return [];
    const busy = state.busy === "offload";
    const make = (label) => h("button", {
      class: "btn btn--sm", type: "button", text: label,
      "aria-busy": busy ? "true" : "false", disabled: busy,
      onClick: () => offload(row),
    });
    if (row.state === "planned") return [make("Offload")];
    if (row.state === "failed" || row.state === "partial") return [make("Retry")];
    return [];
  }

  function filesBody(row) {
    const read = state.files.get(row.plan);
    if (!read || read.loading) return h("p", { class: "quiet", text: "Reading the files" });
    if (!read.items.length) return h("p", { class: "quiet", text: "The plan holds no files." });
    const body = h("div", { class: "ingest__files" },
      read.items.map((item) => h("div", { class: "ingest__file", dataset: { skip: item.action === "skip" ? "true" : "false" } },
        h("span", { class: "ingest__file-name", title: item.rel, text: `‎${item.rel}` }),
        h("span", { text: item.kind || "" }),
        h("span", { class: "num", text: bytes(item.size) }))));
    const left = (read.files || read.items.length) - read.items.length;
    if (left > 0) body.append(h("p", { class: "ingest__more", text: `and ${left} more` }));
    return body;
  }

  /* ---- keys ---- */

  /* Keys belong to this page when focus is on the page or inside it, never to a menu,
     an overlay or the sidebar. */
  function inScope(target) {
    if (!target || target === document.body || target === document.documentElement) return true;
    return typeof el.contains === "function" ? el.contains(target) : true;
  }

  function onKey(event) {
    if (event.defaultPrevented || event.altKey || event.metaKey || event.ctrlKey || !inScope(event.target)) return;
    const tag = (event.target && event.target.tagName) || "";
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if (event.key === "Escape") {
      if (closeRow()) event.preventDefault();
      return;
    }
    const down = event.key === "ArrowDown" || event.key === "j";
    const up = event.key === "ArrowUp" || event.key === "k";
    if (!down && !up && event.key !== "Home" && event.key !== "End") return;
    const nodes = [...rowNodes.values()];
    if (!nodes.length) return;
    event.preventDefault();
    const index = nodes.indexOf(event.target);
    let next = index < 0 ? 0 : down ? Math.min(nodes.length - 1, index + 1) : up ? Math.max(0, index - 1) : 0;
    if (event.key === "End") next = nodes.length - 1;
    nodes[next].focus();
  }
  document.addEventListener("keydown", onKey);

  /* ---- live: an event patches the row it names ---- */

  let timer = 0;
  let disposed = false;

  function again() {
    if (disposed || timer) return;
    timer = setTimeout(() => {
      timer = 0;
      if (!state.busy) load();
    }, 150);
  }

  function rowOfTransfer(id) {
    return state.rows.find((row) => row.transfer === id) || null;
  }

  function onEvent(event) {
    const data = (event && event.data) || {};
    const row = data.id ? rowOfTransfer(data.id) : null;
    if (event.kind === "ferry.transfer.progress" && row) {
      Object.assign(row, { state: "running", fraction: data.fraction, done: data.done, total: data.total, note: data.note || null });
      if (!paintProgress(row)) draw();
      if (current() && current().id === row.id) drawPanel();
      return;
    }
    if (event.kind === "ferry.transfer.started" && row) {
      Object.assign(row, { state: "running", started: data.started, machine: data.machine || row.machine, when: data.started || row.when });
      draw();
      return;
    }
    if (event.kind === "ferry.transfer.finished" && row) {
      Object.assign(row, {
        state: TRANSFER_STATE[data.status] || "done", failed: data.failed || 0, verified: data.verified || 0,
        errors: data.errors || [], ended: data.ended, when: data.ended || row.when, fraction: data.status === "copied" ? 1 : row.fraction,
        bytes: data.bytes || row.bytes,
      });
      draw();
      return;
    }
    if (event.kind === "ferry.receipt.recorded" && data.transfer && rowOfTransfer(data.transfer)) {
      Object.assign(rowOfTransfer(data.transfer), { receipt: data.id, digest: data.digest || null });
      if (current()) drawPanel();
      return;
    }
    again();
  }

  let unsubscribe = null;
  if (typeof api.subscribe === "function") {
    unsubscribe = api.subscribe(["ferry.plan.created", "ferry.transfer.*", "ferry.receipt.recorded"], onEvent);
  }
  draw();
  load();

  return function unmount() {
    disposed = true;
    if (timer) clearTimeout(timer);
    if (typeof unsubscribe === "function") unsubscribe();
    document.removeEventListener("keydown", onKey);
    if (api.toolbar) api.toolbar(null);
    if (api.sidebar) api.sidebar(null);
    clear(el);
  };
}

export default { mount };
