// Gallery page tests.
//
// Zero dependencies, no package.json, no build step: `node --test` runs this
// as-is. That matters because the frontend is served by nginx straight off
// disk and must never gain a Node toolchain -- this file is a development aid
// only and is not part of the serving path. Do not make app.js import
// anything.
//
// The page's inline script is extracted from gallery.html and executed against
// a minimal DOM stub, so these assertions run the REAL shipped code rather than
// a transcription of it. A transcription would let the two drift, which is how
// the timezone bug below was nearly shipped twice.
//
//   node --test frontend/web_ui/test/

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const STATIC = path.join(__dirname, "..", "static");

// -----------------------------------------------------------------------------
// Minimal DOM stub
// -----------------------------------------------------------------------------

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this._text = "";
    this.className = "";
    this.hidden = false;
    this.open = false;
    // In a real DOM classList is a live view over className, not a separate
    // store. Keeping them in sync here is what lets a test assert both
    // tile.classList.has(x) and a querySelectorAll(".x") count.
    const self = this;
    this.classList = {
      has(name) {
        return String(self.className).split(/\s+/).includes(name);
      },
      add(name) {
        if (!this.has(name)) {
          self.className = String(self.className).trim()
            ? self.className + " " + name
            : name;
        }
      },
      remove(name) {
        self.className = String(self.className)
          .split(/\s+/)
          .filter((c) => c && c !== name)
          .join(" ");
      },
    };
  }

  get textContent() {
    if (this.children.length === 0) return this._text;
    return this.children.map((c) => c.textContent).join("");
  }

  set textContent(v) {
    this.children = [];
    this._text = String(v);
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attributes, name)
      ? this.attributes[name]
      : null;
  }

  // The page assigns image.src as a property, which is correct DOM usage.
  // Mirror it onto the attribute store so getAttribute/removeAttribute -- the
  // two the page and these tests actually use -- stay coherent.
  get src() {
    return this.attributes.src === undefined ? "" : this.attributes.src;
  }

  set src(value) {
    this.attributes.src = String(value);
  }

  removeAttribute(name) {
    delete this.attributes[name];
  }

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  replaceChildren(...nodes) {
    this.children = nodes;
  }

  addEventListener(name, fn) {
    (this.listeners[name] = this.listeners[name] || []).push(fn);
  }

  removeEventListener() {}

  // Minimal descendant walk: only the selectors the page actually uses.
  querySelector(sel) {
    if (sel === "[data-status-label]") {
      if (this.tagName === "SPAN" && this.className.includes("status-chip__dot")) {
        return null;
      }
      return this._label();
    }
    return null;
  }

  _label() {
    if (this._cachedLabel) return this._cachedLabel;
    const label = new El("span");
    label.textContent = "";
    Object.defineProperty(label, "_isStatusLabel", { value: true });
    this._cachedLabel = label;
    return label;
  }

  querySelectorAll(sel) {
    const out = [];
    const want = sel.replace(/^\./, "");
    const walk = (node) => {
      for (const c of node.children) {
        if (c.className && String(c.className).split(/\s+/).includes(want)) {
          out.push(c);
        }
        walk(c);
      }
    };
    walk(this);
    return out;
  }

  fire(name, event) {
    (this.listeners[name] || []).forEach((fn) => fn(event || { target: this }));
  }
}

class TextNode {
  constructor(text) {
    this._text = String(text);
    this.children = [];
    this.className = "";
  }
  get textContent() {
    return this._text;
  }
}

function makeDoc() {
  const ids = {};
  const doc = {
    hidden: false,
    createElement: (tag) => new El(tag),
    createTextNode: (t) => new TextNode(t),
    getElementById: (id) => {
      if (!ids[id]) ids[id] = new El("div");
      return ids[id];
    },
    addEventListener() {},
    removeEventListener() {},
  };
  doc._ids = ids;
  return doc;
}

// The real helpers, so the page runs against the shipped app.js contract.
// `doc` must be passed in: witsaba.el closes over the `document` that was
// visible when app.js was evaluated, so loading it against a sandbox with its
// own undefined document makes every element build fail.
function loadAppJs(doc) {
  const src = fs.readFileSync(path.join(STATIC, "assets", "app.js"), "utf8");
  const sandbox = {
    window: {},
    document: doc,
    fetch: undefined,
    setTimeout,
    clearTimeout,
    setInterval,
    clearInterval,
    AbortController,
    URL,
    URLSearchParams,
  };
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox, { filename: "app.js" });
  return sandbox.window.witsaba;
}

/**
 * Boot gallery.html's inline script against a stub DOM.
 * `api` maps a path prefix to the value witsaba.api should resolve with.
 */
function bootPage({ api: routes, search = "" } = {}) {
  const html = fs.readFileSync(path.join(STATIC, "gallery.html"), "utf8");

  // The inline script is the last <script> block with no src attribute.
  const blocks = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/g)];
  assert.ok(blocks.length === 1, "expected exactly one inline script in gallery.html");
  const inline = blocks[0][1];

  const doc = makeDoc();
  const witsaba = loadAppJs(doc);

  const calls = [];
  const fakeApi = (url, opts) => {
    calls.push(url);
    for (const [prefix, value] of Object.entries(routes || {})) {
      if (url.startsWith(prefix)) {
        return typeof value === "function" ? value(url) : Promise.resolve(value);
      }
    }
    return Promise.reject(new Error("no route stubbed for " + url));
  };

  // The page only needs setStatus/showState from the real helper set; keep the
  // rest real so an accidental new dependency shows up as a failure.
  const witsabaStub = Object.assign(Object.create(witsaba), {
    api: fakeApi,
    onVisible: (cb) => cb(), // run once, synchronously triggering refresh()
  });

  const sandbox = {
    document: doc,
    witsaba: witsabaStub,
    window: {},
    location: { search, href: "http://pi.local:4173/gallery" + search, host: "pi.local:4173", protocol: "http:" },
    history: { replaceState: (a, b, url) => { sandbox.history.last = String(url); } },
    console,
    setTimeout,
    clearTimeout,
    setInterval,
    clearInterval,
    URL,
    URLSearchParams,
    encodeURIComponent,
    Promise,
    Number,
    Array,
    Object,
  };
  sandbox.window = sandbox;
  sandbox.globalThis = sandbox;

  vm.createContext(sandbox);
  vm.runInContext(inline, sandbox, { filename: "gallery.html#inline" });

  // ShowModal is how the page opens the lightbox; the stub needs it.
  const lb = doc.getElementById("lightbox");
  lb.showModal = function () {
    this.open = true;
  };
  lb.close = function () {
    this.open = false;
  };

  return { doc, sandbox, calls, html, inline, witsaba };
}

/* The page loads its data through promises, so booting is not done when the
 * inline script returns. Tests await this to let the whole refresh() chain
 * settle; a macrotask hop drains every pending microtask in the chain. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

// The image_url is derived from the tick. Hardcoding one tick here once
// masked a wrong-href bug behind a correct caption, so the fixture builds it
// the way the server does.
const shot = (mac, name, time) => ({
  mac,
  name,
  bytes: 1024,
  image_url: `/api/gallery/img?date=2026-10-05&t=${time}&mac=${mac}`,
});

const DAY = {
  date: "2026-10-05",
  cameras: [
    { mac: "e08cfe3091b0", name: "Backyard" },
    { mac: "d4e9f48d381c", name: "" },
  ],
  moments: [
    {
      time: "14-30-00",
      shots: [shot("e08cfe3091b0", "Backyard", "14-30-00"), shot("d4e9f48d381c", "", "14-30-00")],
    },
    {
      time: "14-45-00",
      shots: [shot("e08cfe3091b0", "Backyard", "14-45-00")],
    },
  ],
};

const DAYS = [
  { date: "2026-10-05", shots: 3, cameras: 2, bytes: 3072 },
  { date: "2026-10-04", shots: 288, cameras: 1, bytes: 999 },
];

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

test("the day picker lists days newest first with shot counts", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const picker = doc.getElementById("day-picker");
  assert.equal(picker.children.length, 2);
  assert.equal(picker.children[0].textContent, "2026-10-05  (3 shots, 2 cameras)");
  assert.equal(picker.children[1].textContent, "2026-10-04  (288 shots, 1 camera)");
  // Newest day is preselected, since the server sorts newest first.
  assert.equal(picker.value, "2026-10-05");
});

test("a single camera is described in the singular", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const picker = doc.getElementById("day-picker");
  assert.match(picker.children[1].textContent, /1 camera\)$/);
});

test("tick times are reformatted as strings and never timezone-shifted", async () => {
  // This is the constraint that broke twice. The tick and the day folder are
  // Pi local wall-clock; handing either to new Date() reinterprets it in the
  // browser's zone, so a UTC browser on a Europe/Madrid Pi renders every
  // capture two hours and possibly one day off from the folder it is filed
  // under. Run this under a non-UTC TZ to make any Date-based regression
  // actually fail rather than silently pass.
  const prev = process.env.TZ;
  process.env.TZ = "Pacific/Kiritimati"; // UTC+14, the worst offset available
  try {
    const { doc } = await bootPage({
      api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
    });
    await settle();
    const host = doc.getElementById("gallery-host");
    const times = host.querySelectorAll("gallery-moment__time").map((n) => n.textContent);
    // Times are reformatted to 12-hour AM/PM: 14-30-00 → "2:30 PM", 14-45-00 → "2:45 PM".
    assert.deepEqual(times, ["2:30 PM", "2:45 PM"]);
    assert.match(doc.getElementById("day-summary").textContent, /^2026-10-05/);
  } finally {
    if (prev === undefined) delete process.env.TZ;
    else process.env.TZ = prev;
  }
});

test("the page never touches innerHTML", async () => {
  const { html, inline } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  for (const [name, src] of [["html", html], ["inline script", inline]]) {
    assert.ok(!/innerHTML|outerHTML|insertAdjacentHTML|document\.write/.test(src),
      name + " uses an HTML-injection sink");
  }
});

test("thumbnails request an allowlisted width and encode their components", async () => {
  const { doc, sandbox } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  void sandbox;
  const host = doc.getElementById("gallery-host");
  const tiles = host.querySelectorAll("gallery-tile");
  assert.equal(tiles.length, 3, "three shots across two moments");

  const imgs = host.querySelectorAll("gallery-tile__image");
  const src = imgs[0].getAttribute("src");
  assert.match(src, /^\/api\/gallery\/thumb\?date=2026-10-05&t=14-30-00&mac=e08cfe3091b0&w=320$/);
  // 320 is the only width the page asks for; the endpoint rejects others with
  // a 400 and offers no clamp.
  const widths = new Set(imgs.map((i) => (i.getAttribute("src").match(/w=(\d+)/) || [])[1]));
  assert.deepEqual([...widths], ["320"]);
});

test("thumbnails are lazy so a 288-frame day does not fetch up front", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  for (const img of doc.getElementById("gallery-host").querySelectorAll("gallery-tile__image")) {
    assert.equal(img.getAttribute("loading"), "lazy");
    assert.equal(img.getAttribute("decoding"), "async");
  }
});

test("camera name falls back to the MAC, which is the only guaranteed label", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const names = doc.getElementById("gallery-host").querySelectorAll("gallery-tile__name");
  assert.deepEqual(names.map((n) => n.textContent), ["Backyard", "d4e9f48d381c", "Backyard"]);
});

test("clicking a tile opens the lightbox at that tile's index", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const lb = doc.getElementById("lightbox");
  const tiles = doc.getElementById("gallery-host").querySelectorAll("gallery-tile");

  // The third tile is the first frame of the second moment. If dayShots were
  // rebuilt per moment instead of per day, this index would be wrong and the
  // arrow keys would walk the wrong moment.
  tiles[2].fire("click", { target: tiles[2], preventDefault() {} });
  assert.equal(lb.open, true);
  assert.equal(doc.getElementById("lb-img").getAttribute("src"),
    "/api/gallery/img?date=2026-10-05&t=14-45-00&mac=e08cfe3091b0");
});

test("arrow keys walk the whole day and stop at the ends", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const lb = doc.getElementById("lightbox");
  const img = doc.getElementById("lb-img");
  const prev = doc.getElementById("lb-prev");
  const next = doc.getElementById("lb-next");
  const tiles = doc.getElementById("gallery-host").querySelectorAll("gallery-tile");

  tiles[0].fire("click", { target: tiles[0], preventDefault() {} });
  assert.equal(prev.hidden, true, "no previous at the first frame");
  assert.equal(next.hidden, false);

  next.fire("click");
  assert.match(img.getAttribute("src"), /t=14-30-00&mac=d4e9f48d381c/);
  assert.equal(prev.hidden, false);
  assert.equal(next.hidden, false);

  next.fire("click");
  assert.match(img.getAttribute("src"), /t=14-45-00/, "walked into the second moment");
  assert.equal(next.hidden, true, "no next at the last frame");
});

test("closing drops the decoded full frame instead of holding it", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const lb = doc.getElementById("lightbox");
  const img = doc.getElementById("lb-img");
  const tiles = doc.getElementById("gallery-host").querySelectorAll("gallery-tile");

  tiles[0].fire("click", { target: tiles[0], preventDefault() {} });
  assert.ok(img.getAttribute("src"), "full frame is loaded while open");
  doc.getElementById("lb-close").fire("click");
  assert.equal(img.getAttribute("src"), null, "src released on close");
  assert.equal(lb.open, false);
});

test("an empty archive renders an empty state, not an error", async () => {
  const { doc } = await bootPage({ api: { "/api/gallery/days": [] } });
  await settle();
  const host = doc.getElementById("gallery-host");
  assert.match(host.textContent, /No captures yet/);
  assert.match(doc.getElementById("day-picker").textContent, /No days captured yet/);
  assert.equal(doc.getElementById("day-picker").disabled, true);
});

test("a day folder with no readable frames renders an empty state", async () => {
  const { doc } = await bootPage({
    api: {
      "/api/gallery/days": DAYS,
      "/api/gallery/day": { date: "2026-10-04", cameras: [], moments: [] },
    },
  });
  await settle();
  assert.match(doc.getElementById("gallery-host").textContent, /No captures on this day/);
});

test("an unreachable API renders an error state with the message", async () => {
  const { doc } = await bootPage({ api: {} });
  await settle();
  const host = doc.getElementById("gallery-host");
  assert.match(host.textContent, /Cannot reach the API/);
  assert.match(doc.getElementById("last-updated").textContent, /last attempt/);
});

test("a broken thumbnail swaps in the no-signal placeholder", async () => {
  const { doc } = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
  });
  await settle();
  const host = doc.getElementById("gallery-host");
  const img = host.querySelectorAll("gallery-tile__image")[0];
  img.fire("error");
  const tile = host.querySelectorAll("gallery-tile")[0];
  assert.ok(tile.classList.has("gallery-tile--broken"));
  // Only the failed tile is decorated, not its siblings.
  assert.equal(host.querySelectorAll("gallery-tile--broken").length, 1);
  // The tile stays a link so the operator can still try the full frame.
  assert.ok(tile.getAttribute("href"));
});

test("?date= selects that day and unknown dates fall back to the newest", async () => {
  const wanted = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
    search: "?date=2026-10-04",
  });
  await settle();
  assert.equal(wanted.doc.getElementById("day-picker").value, "2026-10-04");
  assert.equal(wanted.sandbox.history.last, "http://pi.local:4173/gallery?date=2026-10-04");

  // A hand-edited date that is not in the archive must not be requested.
  const bogus = await bootPage({
    api: { "/api/gallery/days": DAYS, "/api/gallery/day": DAY },
    search: "?date=1999-01-01",
  });
  await settle();
  assert.equal(bogus.doc.getElementById("day-picker").value, "2026-10-05");
  assert.ok(!bogus.calls.some((c) => c.includes("1999-01-01")),
    "an unlisted date must never reach the API");
});

test("every page links to the gallery and the gallery links back", async () => {
  for (const page of ["index", "devices", "stream"]) {
    const src = fs.readFileSync(path.join(STATIC, page + ".html"), "utf8");
    const nav = src.slice(src.indexOf('class="top-bar__nav"'), src.indexOf("</nav>"));
    assert.match(nav, /href="\/gallery"/, page + ".html has no gallery nav link");
  }
  const self = fs.readFileSync(path.join(STATIC, "gallery.html"), "utf8");
  const nav = self.slice(self.indexOf('class="top-bar__nav"'), self.indexOf("</nav>"));
  assert.match(nav, /href="\/gallery" aria-current="page"/);
});

test("every witsaba helper the page calls is exported by app.js", async () => {
  const appSrc = fs.readFileSync(path.join(STATIC, "assets", "app.js"), "utf8");
  const inline = fs.readFileSync(path.join(STATIC, "gallery.html"), "utf8");
  const block = inline.slice(inline.lastIndexOf("<script>"));
  const used = [...block.matchAll(/witsaba\.([A-Za-z_$][\w$]*)/g)].map((m) => m[1]);
  assert.ok(used.length > 0, "no witsaba calls found; the extraction is wrong");
  for (const name of [...new Set(used)]) {
    assert.match(appSrc, new RegExp("\\b" + name + "\\s*:"),
      "gallery.html calls witsaba." + name + ", which app.js does not export");
  }
});