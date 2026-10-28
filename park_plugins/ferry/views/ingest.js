/*
 * Ferry's Ingest view: the sources ferry has planned from, the plans, the transfers
 * running them and the receipt each transfer leaves.
 *
 * mount(el, api) is the whole contract. Data comes from ferry's read ops and every
 * refresh is triggered by the ferry events park streams, so the view never polls.
 */

const STYLE_ID = "ferry-ingest-style";

const CSS = `
.ferry-ingest { display: flex; flex-direction: column; gap: 18px; padding: 18px;
  color: var(--text-primary); font: inherit; }
.ferry-ingest h2 { font-size: 13px; font-weight: 600; letter-spacing: 0.04em;
  text-transform: uppercase; color: var(--text-muted); margin: 0 0 8px; }
.ferry-ingest .bar { display: flex; align-items: baseline; gap: 10px; }
.ferry-ingest .bar h1 { font-size: 18px; font-weight: 600; margin: 0; }
.ferry-ingest .quiet { color: var(--text-muted); font-size: 12px; }
.ferry-ingest .panel { background: var(--bg-frame); border: 1px solid var(--border);
  border-radius: 8px; padding: 12px 14px; }
.ferry-ingest table { width: 100%; border-collapse: collapse; font-size: 13px; }
.ferry-ingest th { text-align: left; font-weight: 500; color: var(--text-faint);
  font-size: 11px; text-transform: uppercase; letter-spacing: 0.05em;
  border-bottom: 1px solid var(--border); padding: 4px 8px 6px 0; }
.ferry-ingest td { padding: 6px 8px 6px 0; border-bottom: 1px solid var(--border);
  vertical-align: middle; }
.ferry-ingest tr.selectable:hover td { background: var(--bg-surface); cursor: pointer; }
.ferry-ingest tr.selected td { background: var(--bg-elevated); }
.ferry-ingest .mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12px; }
.ferry-ingest .state { font-size: 12px; padding: 1px 7px; border-radius: 999px;
  border: 1px solid var(--border-strong); }
.ferry-ingest .state.queued { color: var(--status-idle); }
.ferry-ingest .state.running { color: var(--status-running);
  border-color: var(--status-running); }
.ferry-ingest .state.copied, .ferry-ingest .state.ok { color: var(--status-ok); }
.ferry-ingest .state.partial { color: var(--status-warn); border-color: var(--status-warn); }
.ferry-ingest .state.failed { color: var(--status-error); border-color: var(--status-error); }
.ferry-ingest .meter { height: 4px; background: var(--bg-inset); border-radius: 2px;
  overflow: hidden; min-width: 90px; }
.ferry-ingest .meter span { display: block; height: 100%; background: var(--accent); }
.ferry-ingest .missing { color: var(--text-faint); font-size: 13px; }
.ferry-ingest .error { color: var(--status-error); font-size: 13px; }
.ferry-ingest button { background: var(--bg-elevated); color: var(--text-primary);
  border: 1px solid var(--border-strong); border-radius: 6px; padding: 4px 10px;
  font: inherit; font-size: 12px; cursor: pointer; }
.ferry-ingest button:hover { border-color: var(--border-edge); }
`;

function node(tag, attrs = {}, children = []) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") el.className = value;
    else if (key === "text") el.textContent = value;
    else if (key.startsWith("on") && typeof value === "function") {
      el.addEventListener(key.slice(2).toLowerCase(), value);
    } else el.setAttribute(key, value);
  }
  for (const child of [].concat(children)) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof globalThis.Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

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

function clock(stamp) {
  if (!stamp) return "";
  const parsed = new Date(stamp);
  if (Number.isNaN(parsed.getTime())) return stamp;
  return parsed.toLocaleString();
}

function state(value, label) {
  return node("span", { class: `state ${value || "idle"}`, text: label || value || "idle" });
}

function meter(fraction) {
  const width = Math.max(0, Math.min(1, Number(fraction) || 0)) * 100;
  const fill = node("span");
  fill.style.width = `${width}%`;
  return node("div", { class: "meter", role: "progressbar", "aria-valuenow": width }, [fill]);
}

function table(headers, rows, empty) {
  if (!rows.length) return node("p", { class: "missing", text: empty });
  const head = node("tr", {}, headers.map((one) => node("th", { text: one })));
  return node("table", {}, [node("thead", {}, [head]), node("tbody", {}, rows)]);
}

export async function mount(el, api) {
  if (!document.getElementById(STYLE_ID)) {
    const style = node("style", { id: STYLE_ID });
    style.textContent = CSS;
    document.head.append(style);
  }
  el.classList.add("ferry-ingest");
  el.textContent = "";

  const state_ = { sources: [], plans: [], transfers: [], receipts: [], selected: null, error: null };
  const header = node("div", { class: "bar" });
  const sourcesBox = node("div", { class: "panel" });
  const plansBox = node("div", { class: "panel" });
  const transfersBox = node("div", { class: "panel" });
  const receiptsBox = node("div", { class: "panel" });
  el.append(header, sourcesBox, plansBox, transfersBox, receiptsBox);

  async function load() {
    try {
      const [sources, plans, transfers, receipts] = await Promise.all([
        api.call("ferry.source.list", {}),
        api.call("ferry.plan.list", { limit: 25 }),
        api.call("ferry.transfer.list", { limit: 25 }),
        api.call("ferry.receipt.list", { limit: 25 }),
      ]);
      state_.sources = sources || [];
      state_.plans = plans || [];
      state_.transfers = transfers || [];
      state_.receipts = receipts || [];
      state_.error = null;
    } catch (problem) {
      state_.error = problem && problem.message ? problem.message : String(problem);
    }
    render();
  }

  async function select(plan) {
    state_.selected = plan && plan.id === state_.selected ? null : plan && plan.id;
    if (state_.selected) {
      try {
        const detail = await api.call("ferry.plan.get", { id: state_.selected });
        state_.plans = state_.plans.map((one) => (one.id === detail.id ? detail : one));
      } catch (problem) {
        state_.error = problem && problem.message ? problem.message : String(problem);
      }
    }
    render();
  }

  function renderHeader() {
    const refresh = node("button", { text: "Refresh", onClick: () => load() });
    header.textContent = "";
    header.append(
      node("div", {}, [
        node("h1", { text: "Ingest" }),
        node("div", {
          class: "quiet",
          text: `${state_.sources.length} sources, ${state_.plans.length} plans, `
            + `${state_.transfers.length} transfers`,
        }),
      ]),
      node("div", { style: "flex:1" }),
      refresh,
    );
    if (state_.error) header.append(node("div", { class: "error", text: state_.error }));
  }

  function renderSources() {
    sourcesBox.textContent = "";
    sourcesBox.append(
      node("h2", { text: "Sources" }),
      table(
        ["Folder", "Plans", "Files", "Size", "Last plan", ""],
        state_.sources.map((source) =>
          node("tr", {}, [
            node("td", { class: "mono", text: source.source }),
            node("td", { text: String(source.plans) }),
            node("td", { text: String(source.files) }),
            node("td", { text: bytes(source.bytes) }),
            node("td", { text: clock(source.last_plan) }),
            node("td", {}, [state(source.mounted ? "copied" : "queued",
              source.mounted ? "mounted" : "not here")]),
          ]),
        ),
        "No source has been planned from yet.",
      ),
    );
  }

  function renderPlans() {
    plansBox.textContent = "";
    const rows = state_.plans.map((plan) =>
      node("tr", {
        class: `selectable${plan.id === state_.selected ? " selected" : ""}`,
        onClick: () => select(plan),
      }, [
        node("td", { text: plan.label }),
        node("td", { class: "mono", text: plan.source }),
        node("td", { text: String(plan.files) }),
        node("td", { text: bytes(plan.bytes) }),
        node("td", { text: clock(plan.created) }),
        node("td", { class: "mono", text: String(plan.fingerprint).slice(0, 10) }),
      ]),
    );
    plansBox.append(node("h2", { text: "Plans" }), table(
      ["Label", "Source", "Files", "Size", "Planned", "Fingerprint"], rows,
      "No plan yet. Plan a card with ferry.plan.",
    ));
    const chosen = state_.plans.find((one) => one.id === state_.selected);
    if (chosen && Array.isArray(chosen.items)) {
      plansBox.append(node("h2", { text: `${chosen.label}: ${chosen.items.length} files`,
        style: "margin-top:14px" }));
      plansBox.append(table(
        ["File", "Lands at", "Size", "Kind", "Action"],
        chosen.items.slice(0, 200).map((item) =>
          node("tr", {}, [
            node("td", { class: "mono", text: item.rel }),
            node("td", { class: "mono", text: item.dest }),
            node("td", { text: bytes(item.size) }),
            node("td", { text: item.kind }),
            node("td", {}, [state(item.action === "skip" ? "queued" : "ok", item.action)]),
          ]),
        ),
        "The plan holds no files.",
      ));
    }
  }

  function renderTransfers() {
    transfersBox.textContent = "";
    transfersBox.append(node("h2", { text: "Progress" }), table(
      ["Transfer", "Plan", "State", "Progress", "Files", "Size", "Ended"],
      state_.transfers.map((transfer) =>
        node("tr", {}, [
          node("td", { class: "mono", text: String(transfer.id).slice(0, 10) }),
          node("td", { class: "mono", text: String(transfer.plan).slice(0, 10) }),
          node("td", {}, [state(transfer.status)]),
          node("td", {}, [
            meter(transfer.fraction),
            node("div", { class: "quiet", text: transfer.note || `${transfer.done}/${transfer.total}` }),
          ]),
          node("td", { text: `${transfer.files} (${transfer.failed} failed)` }),
          node("td", { text: bytes(transfer.bytes) }),
          node("td", { text: clock(transfer.ended) }),
        ]),
      ),
      "No transfer has been queued yet.",
    ));
  }

  function renderReceipts() {
    receiptsBox.textContent = "";
    receiptsBox.append(node("h2", { text: "Receipts" }), table(
      ["Receipt", "Kind", "State", "Files", "Size", "Digest", "Written"],
      state_.receipts.map((receipt) =>
        node("tr", {}, [
          node("td", { class: "mono", text: String(receipt.id).slice(0, 10) }),
          node("td", { text: receipt.kind }),
          node("td", {}, [state(receipt.status)]),
          node("td", { text: String(receipt.files) }),
          node("td", { text: bytes(receipt.bytes) }),
          node("td", { class: "mono", text: String(receipt.digest || "").slice(0, 10) }),
          node("td", { text: clock(receipt.created) }),
        ]),
      ),
      "No receipt yet. A finished transfer writes one.",
    ));
  }

  function render() {
    renderHeader();
    renderSources();
    renderPlans();
    renderTransfers();
    renderReceipts();
  }

  let unsubscribe = null;
  if (typeof api.subscribe === "function") {
    unsubscribe = api.subscribe(
      ["ferry.plan.created", "ferry.transfer.*", "ferry.receipt.recorded"],
      () => load(),
    );
  }
  await load();

  return {
    unmount() {
      if (typeof unsubscribe === "function") unsubscribe();
      el.textContent = "";
      el.classList.remove("ferry-ingest");
    },
  };
}
