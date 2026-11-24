/* This screen ships in Ferry's own repo, so it cannot import Park's shared parts by path.
   Park hands them to `mount` as `api.kit`. Everything that draws is made inside `screen()`,
   once per mount, from that mount's parts: nothing about a mount is kept at module level,
   so two of these on screen at once, or one mounted again, never share or overwrite them. */


/*
 * Ferry's Ingest view: every card that has come in, as one dense list.
 *
 * A row is one offload: the card, where it lands, how much, and where it stands (planned,
 * queued, copying, copied, partial, failed), with its transfer and receipt already joined
 * by ferry.ingest.list, so the page is drawn by one read. Rows are grouped by what they
 * need from D: in progress, needs a look, planned, done. A proxy or Resolve receipt is a
 * row of its own. The header's Card filter narrows the list to one card; a row opens in
 * the inspector, which holds the paths, the copy's figures, the problems and the files.
 *
 * The page's one action is "Plan a card": a folder and where it lands. A planned offload
 * is queued, and a failed one retried, from its inspector.
 *
 * Events patch the rows they name (progress costs no request); only a new plan or a new
 * transfer reads again. Keys: the arrows walk the list, Enter opens, Escape closes; a row
 * takes the shared actions menu.
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
.ingest__plan { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)) auto auto; gap: var(--space-2);
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
.ingest__more { color: var(--ink-4); font-size: var(--text-xs); padding-top: var(--space-1); }

@media (max-width: 1080px) {
  .ingest--open { grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr) minmax(0, 55%); }
  .ingest--open .ingest__panel { border-top: 1px solid var(--rule-1); }
  .ingest__plan { grid-template-columns: minmax(0, 1fr) auto auto; }
}
`;

function screen(kit) {
const { element: h, agoShort: ago, bytes: byteSize, clear, svg } = kit.dom;
const { bindItem } = kit.actions;
const { openReview, reviewReturn, reviewable } = kit.review;
const { readPreference, writePreference } = kit.preferences;

/* ---- workspace marks ---- */


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

/* A size the plan has not counted yet reads as nothing copied: 0 B. */
const bytes = (count) => byteSize(Number(count) || 0);

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
  canceled: { tone: "warn", word: "canceled", group: "look" },
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

function mount(el, api, route) {
  installStyles();
  if (api.sidebar) api.sidebar(null);
  const params = ((route && route.params) || (api.route ? api.route().params : {}) || {});
  const project = params.project || "";
  const scope = project ? { project } : {};
  const preference = `ferry-ingest:${project}`;
  const saved = readPreference(api, preference, {});
  let disposed = false;
  let readVersion = 0;
  const active = () => !disposed && (api.isCurrentIdentity?.() ?? true);

  const state = {
    sources: [],
    rows: [],
    more: false,
    card: params.source || "",     // narrows the list to one card
    open: params.plan || reviewReturn(api)?.ferryPlan || saved.pending?.plan || "",
    files: new Map(),              // plan id -> { items, files } read when its row opened
    receipts: new Map(),
    draft: { source: "", destination: "", machine: "", ...saved.draft },
    pending: saved.pending || null,
    machines: null,
    machinesError: "",
    planning: false,               // the "plan a card" form is open
    busy: "",
    error: "",
    loadError: "",
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
    const version = ++readVersion;
    try {
      const read = await api.call("ferry.ingest.list", scope);
      if (!active() || version !== readVersion) return;
      state.sources = (read && read.sources) || [];
      state.rows = (read && read.rows) || [];
      state.more = Boolean(read && read.more);
      state.loadError = "";
      const pending = state.pending && state.rows.find((row) => row.plan === state.pending.plan);
      if (pending?.job && pending.transfer !== state.pending.previous) {
        setPending(null);
        state.error = "";
      }
    } catch (error) {
      if (!active() || version !== readVersion) return;
      state.loadError = message(error);
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
      if (!active()) return;
      state.files.set(plan, { items: (read && read.items) || [], files: (read && read.files) || 0 });
    } catch (error) {
      if (!active()) return;
      state.files.set(plan, { error: message(error) });
    }
    if (current() && current().plan === plan) drawPanel();
  }

  async function readReceipt(id, retry = false) {
    if (!id || (state.receipts.has(id) && !retry)) return;
    const request = { loading: true };
    state.receipts.set(id, request);
    try {
      const receipt = await api.call("ferry.receipt.get", { id });
      if (!active() || state.receipts.get(id) !== request) return;
      if (!receipt || receipt.id !== id) throw new Error("Park did not return this receipt");
      state.receipts.set(id, { receipt });
    } catch (error) {
      if (!active() || state.receipts.get(id) !== request) return;
      state.receipts.set(id, { error: message(error) });
    }
    if (current()?.receipt === id) drawPanel();
  }

  async function readMachines() {
    state.machinesError = "";
    try {
      const machines = await api.call("machines.list", {});
      if (!active()) return;
      state.machines = (machines || []).filter((machine) => machine.kind !== "printer"
        && !machine.policy?.hold && (!machine.policy?.plugins?.length || machine.policy.plugins.includes("ferry")));
    } catch (error) {
      if (!active()) return;
      state.machinesError = message(error);
    }
    if (state.planning) drawTop();
  }

  function current() {
    return state.rows.find((row) => row.id === state.open) || null;
  }

  /* ---- what the page can do ---- */

  function remember() {
    writePreference(api, preference, { draft: state.draft, pending: state.pending });
  }

  function setPending(value) {
    state.pending = value;
    remember();
  }

  function openPlanning(values = {}) {
    state.draft = { ...state.draft, ...values };
    state.planning = true;
    state.error = "";
    remember();
    draw();
    if (!state.machines) readMachines();
  }

  async function planCard() {
    const { source, destination, machine } = state.draft;
    if (state.busy) return;
    if (!source.trim() || !destination.trim() || !machine) {
      state.error = "Choose a worker and enter the card and destination folders.";
      drawTop();
      return;
    }
    state.busy = "plan";
    state.error = "";
    drawTop();
    drawTools();
    try {
      const plan = await api.call("ferry.plan", {
        ...scope, source: source.trim(), destination: destination.trim(), machine,
      }, { timeoutMs: 125_000 });
      if (!active()) return;
      if (!plan?.id || plan.machine !== machine) throw new Error("Park did not return the worker-owned plan");
      state.planning = false;
      state.busy = "";
      state.open = plan && plan.id ? plan.id : state.open;
      await load();
    } catch (error) {
      if (!active()) return;
      state.busy = "";
      state.error = `Couldn’t confirm the plan: ${message(error)}`;
      draw();
    }
  }

  async function offload(row) {
    if (!row?.owner || state.busy || (state.pending && state.pending.plan !== row.plan)) return;
    state.busy = "offload";
    state.error = "";
    const pending = state.pending;
    const key = () => Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16)).join("-");
    const intent = pending || { plan: row.plan, previous: row.transfer, key: key() };
    setPending(intent);
    drawPanel();
    try {
      const transfers = pending ? await api.call("ferry.transfer.list", { plan: row.plan, limit: 1 }) : [];
      if (!active()) return;
      const existing = (transfers || []).find((item) => item.job && item.id !== intent.previous);
      if (!existing) {
        const result = await api.call("ferry.offload", { plan: row.plan }, { requestKey: intent.key });
        if (!active()) return;
        if (result?.queued === false && !result.job) {
          setPending(null);
          state.busy = "";
          state.error = "Park did not accept a copy job.";
          draw();
          return;
        }
        if (!result?.queued || !result.job || !result.transfer || result.plan !== row.plan || result.machine !== row.owner) {
          throw new Error("Park did not confirm a copy job on the planned worker");
        }
        api.toast?.(`${row.label} accepted by the copy queue`, "ok");
      }
      setPending(null);
      state.busy = "";
      await load();
    } catch (error) {
      if (!active()) return;
      state.busy = "";
      if (!pending && error.status >= 400 && error.status < 500 && error.status !== 408) {
        setPending(null);
        state.error = message(error);
      } else {
        state.error = `Copy job unconfirmed. Check the transfer before starting another copy. ${message(error)}`;
      }
      draw();
    }
  }

  async function fileReceipt(id) {
    if (state.busy) return;
    state.busy = "filing";
    state.error = "";
    drawPanel();
    try {
      const filing = await api.call("ferry.receipt.file", { id });
      if (!active()) return;
      if (!Array.isArray(filing?.attached)) throw new Error("Park did not confirm project filing");
      if (filing.errors?.length) state.error = filing.errors.map((item) => item.message).join(". ");
    } catch (error) {
      if (!active()) return;
      state.error = `Couldn’t confirm project filing: ${message(error)}`;
    }
    state.busy = "";
    await readReceipt(id, true);
    if (active()) draw();
  }

  /* ---- the page ---- */

  function visible() {
    return state.rows.filter((row) => !state.card || row.source === state.card
      || JSON.stringify([row.source, row.owner]) === state.card);
  }

  function draw() {
    root.classList.toggle("ingest--open", Boolean(current()));
    drawTop();
    drawList();
    drawPanel();
    drawTools();
    if (current()) {
      if (current().kind === "offload") readFiles(current().plan);
      readReceipt(current().receipt);
    }
  }

  function drawTop() {
    clear(top);
    if (api.ui && !state.reading && (state.rows.length || state.sources.length)) top.append(api.ui.statLine(stats()));
    if (state.loadError || state.error) {
      top.append(h("div", { class: "ingest__note ingest__note--bad", role: "alert" },
        glyph("bad", 8), h("span", { text: state.error || state.loadError }),
        state.loadError && h("button", { class: "btn btn--sm", type: "button", text: "Try again", style: { marginLeft: "auto" }, onClick: () => load() })));
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
    const field = (name, label, placeholder) => {
      const input = h("input", { class: "input mono", type: "text", placeholder, "aria-label": label, disabled: state.busy === "plan" });
      input.value = state.draft[name];
      input.addEventListener("input", () => {
        state.draft[name] = input.value;
        remember();
      });
      return input;
    };
    const source = field("source", "The card's folder", "/Volumes/CARD_A");
    const destination = field("destination", "Where the files land", "/Volumes/Footage/Project");
    const machines = state.machines || [];
    const worker = h("select", { class: "input", "aria-label": "Worker", disabled: state.busy === "plan" },
      h("option", { value: "", text: state.machines ? "Choose worker" : "Checking workers…" }),
      state.draft.machine && !machines.some((machine) => machine.id === state.draft.machine)
        ? h("option", { value: state.draft.machine, text: `${state.draft.machine} (unavailable)` }) : null,
      machines.map((machine) => h("option", { value: machine.id, text: `${machine.name || machine.id}${machine.state === "offline" ? " (offline)" : ""}` })));
    worker.value = state.draft.machine;
    worker.addEventListener("change", () => {
      state.draft.machine = worker.value;
      remember();
    });
    const submit = () => {
      state.draft = { source: source.value, destination: destination.value, machine: worker.value };
      remember();
      planCard();
    };
    const keys = (event) => {
      if (event.key === "Enter") submit();
      if (event.key === "Escape") {
        state.planning = false;
        draw();
      }
    };
    source.addEventListener("keydown", keys);
    destination.addEventListener("keydown", keys);
    return h("div", { class: "ingest__plan" },
      h("label", { class: "field" }, h("span", { class: "field__label", text: "Card" }), source),
      h("label", { class: "field" }, h("span", { class: "field__label", text: "Lands in" }), destination),
      h("label", { class: "field" }, h("span", { class: "field__label", text: "Worker" }), worker),
      h("button", { class: "btn btn--sm", type: "button", text: "Cancel", onClick: () => {
        state.planning = false;
        draw();
      } }),
      h("button", {
        class: "btn btn--sm btn--primary", type: "button", text: state.busy === "plan" ? "Planning…" : "Plan",
        "aria-busy": state.busy === "plan" ? "true" : "false",
        disabled: state.busy === "plan" || !machines.length,
        onClick: submit,
      }),
      state.machinesError ? h("div", { class: "ingest__plan-note", role: "alert" },
        h("span", { text: state.machinesError }),
        h("button", { class: "btn btn--sm", type: "button", text: "Retry workers", onClick: readMachines }))
        : h("p", { class: "quiet ingest__plan-note", text: state.machines && !machines.length
          ? "No Ferry worker is available." : "Choose the worker with both folders mounted. Use an existing destination folder." }));
  }

  /* The header: which card the list shows, and the page's one action. */
  let toolsKey = "";
  function drawTools() {
    if (!api.toolbar) return;
    const key = `${state.planning}|${state.busy}|${state.card}|${state.sources.map((one) => `${one.source}:${one.plans}:${one.mounted}`).join(",")}`;
    if (key === toolsKey) return;
    toolsKey = key;
    if (state.planning) {
      api.toolbar(null);
      return;
    }
    const plan = h("button", {
      class: "btn btn--sm",
      type: "button",
      onClick: () => openPlanning(),
    }, icon("plus", 13), h("span", { text: "Plan a card" }));
    api.toolbar(state.sources.length > 1 || state.card ? [cardFilter(), plan] : plan);
  }

  /* The cards, as the header's filter: one that is not mounted says so. */
  function cardFilter() {
    if (!api.ui || !api.ui.filterMenu) return null;
    return api.ui.filterMenu({
      label: "Card",
      value: state.card,
      all: "Every card",
      options: state.sources.map((source) => ({
        value: source.machine ? JSON.stringify([source.source, source.machine]) : source.source,
        label: `${source.label || source.source}${source.machine ? ` · ${source.machine}` : ""}${source.mounted === false ? " (not here)" : ""}`,
        count: source.plans,
      })),
      onSelect: (value) => {
        state.card = value;
        draw();
      },
    });
  }

  /* ---- the list ---- */

  const rowNodes = new Map();

  function drawList() {
    const focused = document.activeElement && document.activeElement.dataset && document.activeElement.dataset.row;
    clear(list);
    rowNodes.clear();
    if (state.reading) return;
    const rows = visible();
    if (!rows.length && state.loadError) return;
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
    /* The shared actions menu on a row (docs/interaction-baseline.md): a right-click, ⌘.;
       the record stays beside the list, which is the work here. */
    bindItem(node, {
      onMenu: api.itemMenu ? (anchor, at) => api.itemMenu(anchor, offloadItem(row), { at }) : null,
      onDetails: () => openRow(row.id),
      api,
      item: () => offloadItem(row),
      swipe: true,
    });
    rowNodes.set(row.id, node);
    return node;
  }

  function offloadItem(row) {
    return {
      type: row.kind || "offload",
      id: row.id,
      title: row.label,
      look: false,
      paths: row.destination ? [{ node: row.machine || "", path: row.destination }] : [],
      open: { label: "Open", run: () => openRow(row.id) },
      details: () => openRow(row.id),
      brief: [`A Ferry ${row.kind || "offload"} of ${row.card || row.label}, ${stateWord(row)}${row.destination ? `, landing at ${row.destination}` : ""}`],
    };
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
        row.mounted === false ? pair("Here", "not mounted") : null,
        pair("Lands in", row.destination, { mono: true, copy: true }),
        row.kind === "offload" ? pair("Conflicts", row.on_conflict) : null,
      ]) });
    }
    if (row.kind === "offload") {
      sections.push({ title: "Copy", body: pairs([
        pair("Files planned", `${row.copies} to copy${row.skips ? `, ${row.skips} to check` : ""}`),
        pair(row.receipt ? "Bytes copied" : "Size", bytes(row.bytes)),
        row.transfer ? pair("Done", `${row.done} of ${row.total}`) : null,
        row.verified ? pair("Verified", row.verified) : null,
        row.skipped ? pair("Already present", row.skipped) : null,
        pair("Machine", row.machine),
        pair("Job", row.job, { mono: true, copy: true }),
        pair("Queue", row.job_status),
        pair("Started", row.started ? ago(row.started) : ""),
        pair("Ended", row.ended ? ago(row.ended) : ""),
        row.attempts > 1 ? pair("Attempts", row.attempts) : null,
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
    if (row.receipt) sections.push({ title: "Receipt", body: receiptBody(row.receipt, row) });
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
    const busy = Boolean(state.busy);
    const make = (label) => h("button", {
      class: "btn btn--sm", type: "button", text: label,
      "aria-busy": state.busy === "offload" ? "true" : "false",
      disabled: busy || (state.pending && state.pending.plan !== row.plan),
      onClick: () => offload(row),
    });
    if (state.pending?.plan === row.plan) return [make("Check transfer")];
    if (!["planned", "failed", "partial", "canceled"].includes(row.state)) return [];
    if (!row.owner) return [h("button", { class: "btn btn--sm", type: "button", text: "Plan on worker", disabled: busy,
      onClick: () => openPlanning({ source: row.source, destination: row.destination, machine: row.machine || "" }) })];
    if (row.state === "planned") return [make("Offload")];
    return [make("Retry copy")];
  }

  function filesBody(row) {
    const read = state.files.get(row.plan);
    if (!read || read.loading) return h("p", { class: "quiet", text: "Reading the files" });
    if (read.error) return h("div", { role: "alert" }, h("p", { text: read.error }),
      h("button", { class: "btn btn--sm", type: "button", text: "Retry files", onClick: () => {
        state.files.delete(row.plan);
        readFiles(row.plan);
        drawPanel();
      } }));
    if (!read.items.length) return h("p", { class: "quiet", text: "The plan holds no files." });
    const body = h("div", { class: "ingest__files" },
      read.items.map((item) => h("div", { class: "ingest__file" },
        h("span", { class: "ingest__file-name", title: item.rel, text: `‎${item.rel}` }),
        h("span", { text: item.action === "skip" ? "Verify existing" : item.kind || "" }),
        h("span", { class: "num", text: bytes(item.size) }))));
    const left = (read.files || read.items.length) - read.items.length;
    if (left > 0) body.append(h("p", { class: "ingest__more", text: `and ${left} more` }));
    return body;
  }

  function receiptBody(id, row) {
    const read = state.receipts.get(id);
    if (!read || read.loading) return h("p", { class: "quiet", text: "Reading the receipt" });
    if (read.error) return h("div", { role: "alert" }, h("p", { text: read.error }),
      h("button", { class: "btn btn--sm", type: "button", text: "Retry receipt", onClick: () => readReceipt(id, true) }));
    const receipt = read.receipt;
    const files = receipt.files || [];
    const filing = receipt.filing;
    const attached = new Set(filing?.attached || []);
    const reviewFiles = files.filter((file) => attached.has(file.sha256) && reviewable(file));
    const ready = files.filter((file) => file.verified).length;
    const counts = ready ? `${ready} verified` : "No verified files";
    const results = files.map((file) => {
      let action = null;
      if (attached.has(file.sha256) && reviewable(file)) {
        action = h("button", { class: "btn btn--sm", type: "button", text: "Review", onClick: () =>
          openReview(api, file, reviewFiles, { ferryPlan: row.id }, { project: filing.project_slug }) });
      } else if (attached.has(file.sha256) && api.inspect) {
        action = h("button", { class: "btn btn--sm", type: "button", text: "Asset details",
          onClick: () => api.inspect(file.sha256) });
      }
      const result = file.verified ? (file.skipped ? "Verified match" : "Verified copy") : "Unverified";
      return h("div", { class: "ingest__receipt-file" },
        pair(file.rel || file.dest, file.sha256, { mono: true, copy: true }),
        h("p", { class: "quiet", text: `${bytes(file.size)} · ${result}` }), action);
    });
    const details = h("details", {}, h("summary", { text: `${files.length} ${plural(files.length, "file")}` }),
      h("div", { class: "ingest__files" }, results));
    return h("div", { class: "ingest__pairs" },
      pair("ID", id, { mono: true, copy: true }),
      pair("Digest", receipt.digest, { mono: true, copy: true }),
      pair("Result", `${counts}${receipt.detail?.canceled ? " · canceled" : ""}`),
      filing && pair("Filed as project sources", `${attached.size} of ${attached.size + filing.pending.length}`),
      filing?.pending.length && h("button", { class: "btn btn--sm", type: "button", text: "Retry project filing",
        disabled: Boolean(state.busy), onClick: () => fileReceipt(id) }),
      attached.size && api.go && h("button", { class: "btn btn--sm", type: "button", text: "Project library",
        onClick: () => api.go("library", { project: filing.project_slug }, { tab: "assets", fresh: true }) }),
      files.length ? details : null);
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
    const down = event.key === "ArrowDown";
    const up = event.key === "ArrowUp";
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
    if (!active() || (event.project && project && event.project !== project && data.project_slug !== project
      && !state.rows.some((row) => row.project === event.project))) return;
    if (event.kind.startsWith("job.")) {
      const jobRow = state.rows.find((row) => row.job === data.id);
      if (!jobRow) return;
      if (jobRow.receipt && ["job.done", "job.failed", "job.canceled"].includes(event.kind)) {
        state.receipts.delete(jobRow.receipt);
      }
      if (!state.busy) again();
      return;
    }
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
        bytes: data.bytes ?? row.bytes, skipped: data.skipped ?? row.skipped,
      });
      draw();
      return;
    }
    if (event.kind === "ferry.receipt.recorded" && data.transfer && rowOfTransfer(data.transfer)) {
      Object.assign(rowOfTransfer(data.transfer), { receipt: data.id, digest: data.digest || null });
      if (current()?.receipt === data.id) {
        drawPanel();
        readReceipt(data.id);
      }
      return;
    }
    again();
  }

  let unsubscribe = null;
  if (typeof api.subscribe === "function") {
    unsubscribe = api.subscribe(["ferry.plan.created", "ferry.transfer.*", "ferry.receipt.recorded",
      "job.requeued", "job.started", "job.done", "job.failed", "job.canceled"], onEvent);
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

return { mount };
}

export function mount(el, api, route) {
  const kit = api?.kit;
  if (!kit?.dom || !kit?.actions || !kit?.review || !kit?.preferences) {
    throw new Error("Ferry's Ingest screen needs Park's shared parts (api.kit), and this Park did not provide them.");
  }
  return screen(kit).mount(el, api, route);
}

export default { mount };
