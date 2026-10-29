// A small DOM, just enough to mount a park plugin view in node and read what it drew.
//
// The views build their own elements with document.createElement and keep their own
// builders, so a stand-in that records children, attributes, classes, text and listeners
// is all a test needs. Nothing here renders: it answers "what did the view put on the page".

class TextNode {
  constructor(text) {
    this.nodeType = 3;
    this.text = String(text);
    this.parentNode = null;
  }
  get textContent() {
    return this.text;
  }
}

class ClassList {
  constructor(owner) {
    this.owner = owner;
  }
  list() {
    return this.owner.className.split(/\s+/).filter(Boolean);
  }
  add(...names) {
    const next = new Set([...this.list(), ...names]);
    this.owner.className = [...next].join(" ");
  }
  remove(...names) {
    this.owner.className = this.list().filter((name) => !names.includes(name)).join(" ");
  }
  toggle(name, force) {
    const has = this.list().includes(name);
    const want = force === undefined ? !has : Boolean(force);
    if (want) this.add(name);
    else this.remove(name);
    return want;
  }
  contains(name) {
    return this.list().includes(name);
  }
}

export class Element {
  constructor(tag) {
    this.nodeType = 1;
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attrs = {};
    this.style = {};
    this.dataset = {};
    this.listeners = {};
    this.className = "";
    this.parentNode = null;
    this.hidden = false;
    this.classList = new ClassList(this);
    this.value = "";
  }

  get firstChild() {
    return this.children[0] || null;
  }

  get textContent() {
    return this.children.map((child) => child.textContent).join("");
  }

  set textContent(value) {
    this.children = [];
    if (value !== "" && value !== null && value !== undefined) this.append(String(value));
  }

  append(...kids) {
    for (const kid of kids) {
      if (kid === null || kid === undefined) continue;
      const node = typeof kid === "string" || typeof kid === "number" ? new TextNode(kid) : kid;
      node.parentNode = this;
      this.children.push(node);
    }
  }

  replaceChildren(...kids) {
    this.children = [];
    this.append(...kids);
  }

  removeChild(kid) {
    this.children = this.children.filter((child) => child !== kid);
    kid.parentNode = null;
    return kid;
  }

  remove() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }

  setAttribute(name, value) {
    this.attrs[name] = String(value);
    if (name === "class") this.className = String(value);
  }

  getAttribute(name) {
    return name in this.attrs ? this.attrs[name] : null;
  }

  removeAttribute(name) {
    delete this.attrs[name];
  }

  hasAttribute(name) {
    return name in this.attrs;
  }

  addEventListener(type, fn) {
    (this.listeners[type] ||= []).push(fn);
  }

  removeEventListener(type, fn) {
    this.listeners[type] = (this.listeners[type] || []).filter((item) => item !== fn);
  }

  dispatch(type, event = {}) {
    const payload = { type, target: this, preventDefault() {}, stopPropagation() {}, ...event };
    for (const fn of this.listeners[type] || []) fn(payload);
  }

  click() {
    this.dispatch("click");
  }

  contains(node) {
    for (let at = node; at; at = at.parentNode) if (at === this) return true;
    return false;
  }

  /* ".a", "tag", "tag.a" and "[data-x]" are enough for what the views ask. */
  matches(selector) {
    const [, tag = "", classes = "", attr = ""] = /^([a-z0-9]*)((?:\.[\w-]+)*)(\[[^\]]+\])?$/i.exec(selector) || [];
    if (tag && this.tagName !== tag.toUpperCase()) return false;
    for (const name of classes.split(".").filter(Boolean)) if (!this.classList.contains(name)) return false;
    if (attr) {
      const [, key, value] = /^\[([\w-]+)(?:=["']?([^"'\]]*)["']?)?\]$/.exec(attr) || [];
      const have = key.startsWith("data-") ? this.dataset[key.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] : this.attrs[key];
      if (have === undefined || (value !== undefined && String(have) !== value)) return false;
    }
    return true;
  }

  querySelector(selector) {
    return findAll(this, (node) => node !== this && node.matches(selector))[0] || null;
  }

  querySelectorAll(selector) {
    return findAll(this, (node) => node !== this && node.matches(selector));
  }

  focus() {}
  blur() {}
  scrollIntoView() {}
}

export function install() {
  const head = new Element("head");
  const registry = new Map();
  globalThis.document = {
    head,
    createElement: (tag) => new Element(tag),
    createElementNS: (_ns, tag) => new Element(tag),
    createTextNode: (text) => new TextNode(text),
    getElementById: (id) => {
      const found = find(head, (node) => node.attrs.id === id || node.id === id);
      return found || registry.get(id) || null;
    },
    querySelector: () => null,
    addEventListener() {},
    removeEventListener() {},
  };
  globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
  return head;
}

/* Every element under `root` (itself included) that the predicate accepts. */
export function findAll(root, predicate, out = []) {
  if (!root || root.nodeType !== 1) return out;
  if (predicate(root)) out.push(root);
  for (const child of root.children) findAll(child, predicate, out);
  return out;
}

export function find(root, predicate) {
  return findAll(root, predicate)[0] || null;
}

export function byClass(root, name) {
  return findAll(root, (node) => node.classList.contains(name));
}

export function byText(root, text, tag) {
  return findAll(root, (node) => (!tag || node.tagName === tag.toUpperCase()) && node.textContent.trim() === text);
}

/* Let the view's reads settle: two turns of the microtask queue and a short timer. */
export async function settle(ms = 30) {
  await new Promise((resolve) => setTimeout(resolve, ms));
}

export function assert(condition, message) {
  if (!condition) {
    console.error(`FAIL: ${message}`);
    process.exit(1);
  }
}

/* The shared components, reduced to nodes the tests can read. */
export function ui() {
  const el = (tag, props, ...kids) => {
    const node = new Element(tag);
    for (const [key, value] of Object.entries(props || {})) {
      if (key === "class") node.className = value;
      else if (key === "text") node.append(value);
      else if (key === "onClick") node.addEventListener("click", value);
      else node.setAttribute(key, value);
    }
    node.append(...kids.flat());
    return node;
  };
  return {
    pageEmpty: (sentence, action) => el("div", { class: "page-empty" }, el("p", { class: "page-empty__line", text: sentence }), action || null),
    statLine: (items) => el("p", { class: "stat-line" }, ...items.filter(Boolean).map((item) =>
      el("span", { class: "stat-line__item", text: typeof item === "string" ? item : `${item.value} ${item.label || ""}`.trim() }))),
    inspectorPanel: ({ title, subtitle, sections = [], actions = [] }) => el("aside", { class: "panel" },
      el("h2", { class: "panel__title", text: title }),
      subtitle ? el("p", { class: "panel__subtitle", text: subtitle }) : null,
      ...[].concat(actions || []).filter(Boolean).map((action) => (action.nodeType ? action : el("button", { text: action.label, onClick: action.onSelect }))),
      ...sections.map((section) => el("section", { class: "panel__section" },
        el("h3", { class: "eyebrow", text: section.title }), ...[].concat(section.body || [])))),
    assetTile: (asset, opts = {}) => {
      const node = el("button", { class: "asset-tile", onClick: () => opts.onOpen && opts.onOpen(asset, node) },
        el("span", { class: "asset-tile__media" }), el("span", { class: "asset-tile__title", text: opts.title || "" }),
        el("span", { class: "asset-tile__meta", text: opts.meta || "" }));
      Object.assign(node.dataset, opts.dataset || {});
      node.setAttribute("aria-selected", opts.selected ? "true" : "false");
      node.release = { stop() {} };
      return node;
    },
    listRow: ({ avatar, title, meta, trailing, onOpen }) => el("button", { class: "list-row", onClick: onOpen },
      el("span", { class: "list-row__avatar" }, avatar || null),
      el("span", { class: "list-row__text" }, el("span", { class: "list-row__title", text: title }),
        meta ? el("span", { class: "list-row__meta", text: meta }) : null),
      trailing ? el("span", { class: "list-row__trailing" }, trailing) : null),
  };
}

/* A park api with the surface views use, recording what they asked for. */
export function makeApi({ routes = {}, params = {}, projects = [] } = {}) {
  const log = { calls: [], sidebars: [], toolbars: [], headers: [], toasts: [], go: [], menus: [], subscriptions: [] };
  const api = {
    log,
    async call(op, args = {}) {
      log.calls.push({ op, args });
      const handler = routes[op];
      if (handler === undefined) throw new Error(`no stub for ${op}`);
      return typeof handler === "function" ? handler(args) : handler;
    },
    route: () => ({ params, dest: "", tab: "", segments: [] }),
    projects: () => projects,
    async project(value) {
      return projects.find((item) => item.slug === value || item.id === value) || null;
    },
    header: (words) => log.headers.push(words),
    toolbar: (content) => log.toolbars.push(content),
    sidebar: (spec) => log.sidebars.push(spec),
    toast: (text, kind) => log.toasts.push([text, kind]),
    go: (...args) => log.go.push(args),
    menu(anchor, items, options) {
      log.menus.push({ anchor, items, options });
      return { close() {} };
    },
    segmented({ options, value, onSelect }) {
      const track = new Element("div");
      track.value = () => value;
      for (const option of options) {
        const button = new Element("button");
        button.append(option.label);
        button.setAttribute("aria-pressed", option.value === value ? "true" : "false");
        button.addEventListener("click", () => onSelect(option.value));
        track.append(button);
      }
      return track;
    },
    inspect() {},
    subscribe(kinds, fn) {
      const entry = { kinds, fn, live: true };
      log.subscriptions.push(entry);
      return () => {
        entry.live = false;
      };
    },
    emit(event) {
      for (const entry of log.subscriptions) {
        if (!entry.live) continue;
        const wanted = entry.kinds.some((kind) => (kind.endsWith("*") ? event.kind.startsWith(kind.slice(0, -1)) : kind === event.kind));
        if (wanted) entry.fn(event);
      }
    },
    ui: ui(),
    media: { fill: () => () => {} },
  };
  return api;
}

export const counts = (api, op) => api.log.calls.filter((call) => call.op === op).length;
