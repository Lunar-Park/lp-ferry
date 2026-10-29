// Mounts Ingest against a stub api and a small DOM and checks what it asks for and draws.
//   node ingest_view_scenarios.mjs <path to views/ingest.js>
import { pathToFileURL } from "node:url";
import { Element, assert, byClass, counts, find, findAll, install, makeApi, settle } from "./view_dom.mjs";

install();
const view = await import(pathToFileURL(process.argv[2]).href);
assert(typeof view.mount === "function", "mount is exported");
const text = (root) => root.textContent.replace(/\s+/g, " ").trim();
const now = new Date().toISOString();

const offload = (id, state, extra = {}) => ({
  kind: "offload", id, plan: id, label: id.toUpperCase(), card: id.toUpperCase(), source: `/Volumes/${id.toUpperCase()}`,
  mounted: true, destination: `/Volumes/Footage/2026-10-28/${id.toUpperCase()}`, destination_name: id.toUpperCase(),
  on_conflict: "skip", files: 12, copies: 12, skips: 0, bytes: 4 * 1024 ** 3, created: now, when: now, state,
  transfer: state === "planned" ? null : `t-${id}`, attempts: state === "planned" ? 0 : 1, receipt: null, digest: null,
  fraction: state === "running" ? 0.4 : null, done: 0, total: 12, failed: 0, verified: 0, errors: [], note: null, machine: null,
  ...extra,
});
const read = {
  sources: [
    { source: "/Volumes/A", label: "A", plans: 1, files: 12, bytes: 1, last_plan: now, mounted: true },
    { source: "/Volumes/B", label: "B", plans: 2, files: 24, bytes: 1, last_plan: now, mounted: false },
  ],
  rows: [
    offload("a", "running"),
    offload("b", "failed", { failed: 2, errors: ["a.mov: the card was pulled", "b.mov: the card was pulled"] }),
    offload("c", "planned"),
    offload("d", "done", { receipt: "r1", digest: "f".repeat(64) }),
    { kind: "proxy", id: "p1", plan: null, label: "Proxies", card: "", source: "", mounted: false, destination: "",
      destination_name: "", files: 4, bytes: 1024, state: "done", receipt: "p1", digest: null, errors: [], failed: 0,
      machine: "titus", ended: now, created: now, when: now },
  ],
  more: false,
};

/* 1. Nothing planned: one read, one sentence, one action. */
{
  const api = makeApi({ routes: { "ferry.ingest.list": { sources: [], rows: [], more: false } } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: {} });
  await settle();
  assert(api.log.calls.length === 1 && api.log.calls[0].op === "ferry.ingest.list", "one request draws an empty Ingest");
  assert(text(el).includes("Nothing has been planned yet."), `the empty state is one sentence: ${text(el)}`);
  const action = api.log.toolbars.filter(Boolean).pop();
  assert(action.textContent.includes("Plan a card") && action.className.includes("btn--primary"), "the page's one action plans a card");
  unmount();
}

/* 2. A busy park: one read draws everything, rows are grouped by what they need. */
{
  const api = makeApi({ routes: { "ferry.ingest.list": read, "ferry.plan.get": { id: "b", files: 3, items: [
    { rel: "DCIM/a.mov", kind: "video", size: 100, action: "copy" }, { rel: "notes.txt", kind: "document", size: 5, action: "skip" }] } } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: {} });
  await settle();
  assert(counts(api, "ferry.ingest.list") === 1 && api.log.calls.length === 1, "one request draws the list");
  const heads = byClass(el, "ingest__group-head").map((node) => node.children[0].textContent);
  assert(heads.join() === "In progress,Needs a look,Planned,Done", `groups, in the order D needs them: ${heads}`);
  const rows = byClass(el, "ingest__row");
  assert(rows.length === 5, "one row per offload and receipt");
  assert(text(el).includes("2 cards") && text(el).includes("4 offloads") && text(el).includes("1 in progress") && text(el).includes("1 needs a look"),
    `the counts are one stat line: ${text(find(el, (n) => n.classList.contains("stat-line")))}`);
  assert(!/[0-9a-f]{20}/.test(text(find(el, (n) => n.classList.contains("ingest__list")))), "no ids or hashes in the list");
  const cards = api.log.sidebars.pop().sections[0];
  assert(cards.title === "Cards" && cards.items.length === 2 && cards.items[1].glyph === "warn", "the cards are the sidebar, and say which are not here");

  /* opening a row reads its files once */
  rows[1].click();
  await settle();
  assert(counts(api, "ferry.plan.get") === 1 && api.log.calls.find((c) => c.op === "ferry.plan.get").args.items_limit === 200,
    "opening a row asks for a page of its files");
  assert(text(el).includes("a.mov: the card was pulled"), "the problems are in the inspector");
  assert(text(el).includes("DCIM/a.mov"), "so are the files");
  assert(text(el).includes("Retry"), "a failed offload can be retried");
  const rowsAgain = byClass(el, "ingest__row");
  rowsAgain[1].click();
  rowsAgain[1].click();
  await settle();
  assert(counts(api, "ferry.plan.get") === 1, "its files are not read twice");

  /* a card narrows the list without a request */
  const before = api.log.calls.length;
  cards.items[0].onSelect();
  await settle();
  assert(byClass(el, "ingest__row").length === 1 && api.log.calls.length === before, "a card narrows the list, reading nothing");
  api.log.sidebars.pop().sections[0].items[0].onSelect();
  await settle();

  /* progress patches the row it names: no request */
  const calls = api.log.calls.length;
  api.emit({ kind: "ferry.transfer.progress", data: { id: "t-a", done: 9, total: 12, fraction: 0.75, note: "" } });
  await settle(250);
  assert(api.log.calls.length === calls, "progress costs no request");
  assert(text(byClass(el, "ingest__row")[0]).includes("75%"), "the row shows the new progress");
  /* a new plan is one read */
  api.emit({ kind: "ferry.plan.created", data: { id: "z" } });
  await settle(300);
  assert(counts(api, "ferry.ingest.list") === 2, "a new plan reads the list once more");
  unmount();
  assert(api.log.subscriptions.every((entry) => !entry.live), "unmount drops the subscription");
}

/* 3. Planning a card sends the folders as typed; offloading a planned card sends its plan. */
{
  const routes = {
    "ferry.ingest.list": read,
    "ferry.plan": { id: "n", label: "NEW" },
    "ferry.offload": { transfer: "t-n" },
    "ferry.plan.get": { id: "c", files: 0, items: [] },
  };
  const api = makeApi({ routes });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: {} });
  await settle();
  api.log.toolbars.filter(Boolean).pop().click();
  await settle();
  const [card, lands] = findAll(el, (node) => node.tagName === "INPUT");
  assert(card && lands, "the form asks for a card and where it lands");
  card.value = "/Volumes/NEW";
  lands.value = "/Volumes/Footage/NEW";
  lands.dispatch("keydown", { key: "Enter" });
  await settle();
  const planned = api.log.calls.find((c) => c.op === "ferry.plan");
  assert(planned && planned.args.source === "/Volumes/NEW" && planned.args.destination === "/Volumes/Footage/NEW", "ferry.plan gets the folders");
  byClass(el, "ingest__row")[2].click();
  await settle();
  const button = find(el, (node) => node.tagName === "BUTTON" && node.textContent === "Offload");
  assert(button, "a planned offload can be offloaded");
  button.click();
  await settle();
  assert(api.log.calls.some((c) => c.op === "ferry.offload" && c.args.plan === "c"), "ferry.offload gets the plan");
  unmount();
}

/* 4. A read that fails is one line, not a blank page. */
{
  const api = makeApi({ routes: { "ferry.ingest.list": () => { throw new Error("the park is not answering"); } } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: {} });
  await settle();
  assert(text(el).includes("the park is not answering"), "the error is said");
  assert(!text(el).includes("Nothing has been planned yet."), "and a failed read is not called an empty park");
  assert(text(el).includes("Try again"), "with the way to try again");
  unmount();
}

console.log("ok");
