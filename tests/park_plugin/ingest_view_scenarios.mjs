// Mounts Ingest against a stub api and a small DOM and checks what it asks for and draws.
//   node ingest_view_scenarios.mjs <path to views/ingest.js> <path to park's tests/app/workspaces/view_dom.mjs>
import { pathToFileURL } from "node:url";

const { Element, assert, byClass, counts, find, findAll, install, makeApi: stubApi, settle, kit: parkKit } =
  await import(pathToFileURL(process.argv[3]).href);

install();
const kit = await parkKit();
const makeApi = (options = {}) => stubApi({ ...options, kit });
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
  owner: "worker-a",
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
  const action = [].concat(...api.log.toolbars.filter(Boolean)).filter(Boolean).pop();
  // Like every header's actions, a standard button: the accent is the composer's and the brand's.
  assert(action.textContent.includes("Plan a card") && !action.className.includes("btn--primary"), "the page's one action plans a card");
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
  const headerCard = () => [].concat(...api.log.toolbars.filter(Boolean)).filter(Boolean).find((node) => node.spec && node.spec.label === "Card");
  const cards = headerCard().spec;
  assert(cards.options.length === 2 && cards.options[1].label.includes("not here"), "the cards are the header's Card filter, and say which are not here");
  assert(!api.log.sidebars.some((spec) => spec && spec.sections && spec.sections.length), "and there is no side list");

  /* opening a row reads its files once */
  rows[1].click();
  await settle();
  assert(counts(api, "ferry.plan.get") === 1 && api.log.calls.find((c) => c.op === "ferry.plan.get").args.items_limit === 200,
    "opening a row asks for a page of its files");
  assert(text(el).includes("a.mov: the card was pulled"), "the problems are in the inspector");
  assert(text(el).includes("DCIM/a.mov"), "so are the files");
  assert(text(el).includes("Verify existing"), "the plan asks Ferry to verify existing bytes");
  assert(text(el).includes("Retry"), "a failed offload can be retried");
  const rowsAgain = byClass(el, "ingest__row");
  rowsAgain[1].click();
  rowsAgain[1].click();
  await settle();
  assert(counts(api, "ferry.plan.get") === 1, "its files are not read twice");

  /* a card narrows the list without a request */
  const before = api.log.calls.length;
  cards.onSelect(cards.options[0].value);
  await settle();
  assert(byClass(el, "ingest__row").length === 1 && api.log.calls.length === before, "a card narrows the list, reading nothing");
  headerCard().spec.onSelect("");
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
  let planning = 0;
  const routes = {
    "ferry.ingest.list": read,
    "machines.list": [{ id: "worker-a", name: "Worker A" }],
    "ferry.plan": () => {
      if (++planning === 1) throw new Error("Destination not mounted");
      return { id: "n", label: "NEW", machine: "worker-a" };
    },
    "ferry.offload": { plan: "c", transfer: "t-c", queued: true, job: "job-c", machine: "worker-a" },
    "ferry.plan.get": { id: "c", files: 0, items: [] },
  };
  const api = makeApi({ routes });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: { project: "ferry-test" } });
  await settle();
  assert(api.log.calls[0].args.project === "ferry-test", "list uses the active project");
  [].concat(...api.log.toolbars.filter(Boolean)).filter(Boolean).pop().click();
  await settle();
  const [card, lands] = findAll(el, (node) => node.tagName === "INPUT");
  assert(card && lands, "the form asks for a card and where it lands");
  card.value = "/Volumes/NEW";
  lands.value = "/Volumes/Footage/NEW";
  find(el, (node) => node.tagName === "SELECT").value = "worker-a";
  lands.dispatch("keydown", { key: "Enter" });
  await settle();
  const planned = api.log.calls.find((c) => c.op === "ferry.plan");
  assert(planned && planned.args.source === "/Volumes/NEW" && planned.args.destination === "/Volumes/Footage/NEW", "ferry.plan gets the folders");
  assert(planned.args.project === "ferry-test" && planned.args.machine === "worker-a", "the plan keeps its project and worker");
  const retryDraft = findAll(el, (node) => node.tagName === "INPUT").map((node) => node.value);
  assert(text(el).includes("Destination not mounted") && retryDraft.join() === "/Volumes/NEW,/Volumes/Footage/NEW", "failed planning keeps its draft");
  find(el, (node) => node.tagName === "BUTTON" && node.textContent === "Plan").click();
  await settle();
  byClass(el, "ingest__row")[2].click();
  await settle();
  const button = find(el, (node) => node.tagName === "BUTTON" && node.textContent === "Offload");
  assert(button, "a planned offload can be offloaded");
  button.click();
  await settle();
  assert(api.log.calls.some((c) => c.op === "ferry.offload" && c.args.plan === "c"), "ferry.offload gets the plan");
  assert(api.log.toasts.length === 1, "only a confirmed queue job gets a toast");
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

/* 5. An unknown submission reads back the specific job and opens its receipt. */
{
  let accepted = false;
  let reads = 0;
  let filed = false;
  const sha256 = "a".repeat(64);
  const id = "recovery";
  const api = makeApi({ routes: {
    "ferry.ingest.list": () => ({ rows: [accepted
      ? offload(id, "done", { job: "job-recovery", receipt: "receipt-recovery" }) : offload(id, "planned")] }),
    "ferry.plan.get": () => {
      if (++reads === 1) throw new Error("Could not read the plan");
      return { items: [{ rel: "notes.txt", size: 12 }], files: 1 };
    },
    "ferry.offload": () => { accepted = true; throw new Error("Lost acknowledgment"); },
    "ferry.transfer.list": [{ id: "t-recovery", job: "job-recovery" }],
    "ferry.receipt.get": () => ({
      id: "receipt-recovery", files: [{ rel: "notes.txt", sha256, size: 12, verified: true, skipped: true }],
      filing: { project_slug: "ferry-test", pending: filed ? [] : [sha256], attached: filed ? [sha256] : [] },
    }),
    "ferry.receipt.file": () => { filed = true; return { attached: [sha256] }; },
  } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: { project: "ferry-test", plan: id } });
  const button = (label) => find(el, (node) => node.tagName === "BUTTON" && node.textContent === label);
  await settle();
  assert(text(el).includes("Could not read the plan"), "file errors replace the loading state");
  button("Retry files").click();
  await settle();
  assert(text(el).includes("notes.txt"), "file reads can be retried");
  button("Offload").click();
  await settle();
  assert(text(el).includes("Copy job unconfirmed") && api.log.toasts.length === 0, "a lost reply is unknown");
  button("Check transfer").click();
  await settle();
  assert(counts(api, "ferry.offload") === 1 && counts(api, "ferry.transfer.list") === 1, "the accepted job is found without a second submission");
  assert(text(el).includes("job-recovery") && text(el).includes(sha256) && text(el).includes("Verified match"), "the exact job and verified file are visible");
  button("Retry project filing").click();
  await settle();
  button("Project library").click();
  assert(api.log.go[0][1].project === "ferry-test", "filed sources link to the correct Library");
  unmount();
}

/* The first plan in an empty project arrives with a core id and a route slug. */
{
  let rows = [];
  const api = makeApi({ routes: { "ferry.ingest.list": () => ({ rows }) } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: { project: "ferry-test" } });
  await settle();
  rows = [offload("first", "planned", { project: "p_1", project_slug: "ferry-test" })];
  api.emit({ kind: "ferry.plan.created", project: "p_2", data: { id: "another", project_slug: "other" } });
  await settle(250);
  assert(counts(api, "ferry.ingest.list") === 1, "a different project does not reload the empty workspace");
  api.emit({ kind: "ferry.plan.created", project: "p_1", data: { id: "first", project_slug: "ferry-test" } });
  await settle(250);
  assert(byClass(el, "ingest__row").length === 1, "the first external plan appears in the scoped workspace");
  unmount();
}

/* A receipt can arrive before the worker has finished filing its verified hashes. */
{
  let filed = false;
  const sha256 = "b".repeat(64);
  const id = "auto-file";
  const api = makeApi({ routes: {
    "ferry.ingest.list": { rows: [offload(id, "done", { job: "job-a", receipt: "r-auto" })] },
    "ferry.plan.get": { items: [], files: 0 },
    "ferry.receipt.get": () => ({
      id: "r-auto", files: [{ rel: "clip.mov", sha256, size: 12, kind: "video", verified: true }],
      filing: { project_slug: "ferry-test", pending: filed ? [] : [sha256], attached: filed ? [sha256] : [] },
    }),
  } });
  const el = new Element("div");
  const unmount = view.mount(el, api, { params: { project: "ferry-test", plan: id } });
  await settle();
  assert(text(el).includes("Retry project filing"), "the initial receipt exposes pending filing");
  filed = true;
  api.emit({ kind: "job.done", data: { id: "job-a" } });
  await settle(250);
  assert(text(el).includes("Project library") && text(el).includes("Review"), "settling the job refreshes the filed receipt links");
  assert(!text(el).includes("Retry project filing"), "the filing action is only shown when repair is needed");
  unmount();
}

console.log("ok");
