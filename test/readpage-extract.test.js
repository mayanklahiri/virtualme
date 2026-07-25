import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { runReadPage } from "./helpers/dom-stub.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const fixtures = path.join(root, "test/fixtures");

// Mirrors the readpage.js semantic keep-set (spec 027 §3c.5).
const SEMANTIC = new Set([
  "h1", "h2", "h3", "h4", "h5", "h6", "a", "img", "video", "audio", "iframe",
  "table", "form", "input", "select", "textarea", "button", "label", "ul", "ol",
  "dl", "li", "p", "blockquote", "pre", "code", "time", "figcaption", "svg", "tr",
]);
const CONTENT_KEYS = [
  "text", "href", "src", "alt", "type", "name", "value", "placeholder",
  "action", "rows", "items", "note",
];

/** @param {any} nodes @param {(node: any) => void} fn */
function walkNodes(nodes, fn) {
  for (const node of nodes ?? []) {
    fn(node);
    walkNodes(node.children, fn);
    walkNodes(node.items, fn);
  }
}

/** @param {any} digest */
function assertDigestInvariants(digest) {
  walkNodes(digest.body, (node) => {
    if (node.note) return;
    // Flattened list items are {text[, href]} without tag (spec 027 §3f).
    if (!node.tag) {
      assert.ok(node.text, `untagged node without text: ${JSON.stringify(node)}`);
      return;
    }
    assert.equal(node.sel, undefined, `sel must not be emitted: ${JSON.stringify(node)}`);
    if (!SEMANTIC.has(node.tag) && !CONTENT_KEYS.some((key) => node[key])) {
      assert.fail(`content-free non-semantic wrapper survived collapse: ${JSON.stringify(node)}`);
    }
    if (node.text) {
      assert.doesNotMatch(node.text, /\s{2,}|[\n\t]/, `text not normalized: ${JSON.stringify(node.text)}`);
    }
  });
}

test("read_page extracts example.com structure deterministically", () => {
  const fixture = JSON.parse(readFileSync(path.join(fixtures, "example-com.dom.json"), "utf8"));
  const first = runReadPage(fixture);
  const second = runReadPage(fixture);
  // Results come from separate vm realms; compare via JSON for determinism.
  assert.equal(JSON.stringify(first), JSON.stringify(second));
  assert.equal(first.title, "Example Domain");
  assert.equal(first.url, "https://example.com/");
  assert.ok(Array.isArray(first.body));
  let headings = 0;
  let links = 0;
  walkNodes(first.body, (node) => {
    assert.ok(node.tag);
    if (node.tag === "h1") headings++;
    if (node.href) {
      links++;
      assert.match(node.href, /^https:\/\//);
    }
  });
  assert.equal(headings, 1);
  assert.equal(links, 1);
  assertDigestInvariants(first);
});

test("read_page extracts lahiri.me links and headings", () => {
  const fixture = JSON.parse(readFileSync(path.join(fixtures, "lahiri-me.dom.json"), "utf8"));
  const digest = runReadPage(fixture);
  assert.match(digest.title, /Lahiri/i);
  let links = 0;
  walkNodes(digest.body, (node) => {
    if (node.href) links++;
  });
  assert.ok(links >= 2);
  assertDigestInvariants(digest);
});

test("hidden and aria-hidden subtrees are pruned with scripts", () => {
  const fixture = {
    title: "Prune", url: "https://example.com/",
    body: [{
      tag: "div", children: [
        { tag: "p", text: "invisible", visible: false },
        { tag: "p", text: "screenreader-off", attrs: { "aria-hidden": "true" } },
        { tag: "script", text: "alert(1)" },
        { tag: "p", text: "shown" },
      ],
    }],
  };
  const digest = runReadPage(fixture);
  /** @type {string[]} */
  const texts = [];
  walkNodes(digest.body, (node) => { if (node.text) texts.push(node.text); });
  assert.deepEqual(texts, ["shown"]);
});

test("nested content-free wrappers hoist their content to the top", () => {
  const fixture = {
    title: "Hoist", url: "https://example.com/",
    body: [{
      tag: "div",
      children: [{ tag: "div", children: [{ tag: "section", children: [{ tag: "p", text: "promoted" }] }] }],
    }],
  };
  const digest = runReadPage(fixture);
  assert.equal(digest.body.length, 1);
  assert.equal(digest.body[0].tag, "p");
  assert.equal(digest.body[0].text, "promoted");
});

test("data tables shape direct rows, preserve links, and enforce the row cap", () => {
  const trs = [];
  for (let index = 0; index < 405; index++) {
    trs.push({ tag: "tr", children: [
      { tag: index === 0 ? "th" : "td", children: [{ tag: "a", text: `r${index} c1`, attrs: { href: `/r/${index}` } }] },
      { tag: "td", text: "c2" },
    ] });
  }
  const fixture = {
    title: "Table", url: "https://example.com/",
    body: [{ tag: "table", children: trs }],
  };
  const digest = runReadPage(fixture);
  const table = digest.body[0];
  assert.equal(table.tag, "table");
  assert.equal(table.children, undefined);
  assert.equal(table.rows.length, 401);
  assert.deepEqual(table.rows[0], [
    { text: "r0 c1", href: "https://example.com/r/0" }, { text: "c2" },
  ]);
  assert.deepEqual(table.rows[400], ["…truncated"]);
});

test("layout tables preserve links and do not flatten nested rows", () => {
  const fixture = {
    title: "Layout", url: "https://example.com/",
    body: [{
      tag: "table", children: [{
        tag: "tbody", children: [{
          tag: "tr", children: [{
            tag: "td", children: [
              { tag: "a", text: "Story", attrs: { href: "/story" } },
              { tag: "table", children: [{
                tag: "tr", children: [{ tag: "td", text: "Score 10 points" }],
              }] },
            ],
          }],
        }],
      }],
    }],
  };
  const digest = runReadPage(fixture);
  assert.equal(digest.body[0].tag, "tr");
  assert.equal(digest.body[0].rows, undefined);
  const serialized = JSON.stringify(digest);
  assert.match(serialized, /https:\/\/example\.com\/story/);
  assert.match(serialized, /Score 10 points/);
});

test("numbered feed rows group title and metadata into articles", () => {
  const fixture = {
    title: "Feed", url: "https://example.com/",
    body: [{ tag: "table", children: [{ tag: "tbody", children: [
      { tag: "tr", children: [
        { tag: "td", text: "1." },
        { tag: "td", children: [{ tag: "a", text: "Story", attrs: { href: "/story" } }] },
      ] },
      { tag: "tr", children: [{ tag: "td", children: [
        { tag: "span", text: "10 points" },
        { tag: "a", text: "3 comments", attrs: { href: "/item?id=1" } },
      ] }] },
      { tag: "tr" },
    ] }] }],
  };
  const digest = runReadPage(fixture);
  assert.equal(digest.body.length, 1);
  assert.equal(digest.body[0].tag, "article");
  assert.equal(digest.body[0].rank, 1);
  assert.equal(digest.body[0].title, "Story");
  assert.equal(digest.body[0].title_link, "[Story](https://example.com/item?id=1)");
  assert.equal(digest.body[0].url, "https://example.com/story");
  assert.equal(digest.body[0].score, "10 points");
  assert.equal(digest.body[0].comments, "3 comments");
  assert.equal(digest.body[0].comment_url, "https://example.com/item?id=1");
});

test("single-link list items flatten; long lists append a truncation note", () => {
  /** @type {any[]} */
  const lis = [{ tag: "li", children: [{ tag: "a", text: "Story one", attrs: { href: "/one" } }] }];
  for (let index = 0; index < 404; index++) {
    lis.push({ tag: "li", text: `item ${index}` });
  }
  const fixture = {
    title: "List", url: "https://example.com/",
    body: [{ tag: "ul", children: lis }],
  };
  const digest = runReadPage(fixture);
  const list = digest.body[0];
  assert.equal(list.tag, "ul");
  assert.equal(list.items.length, 401);
  assert.equal(list.items[0].text, "Story one");
  assert.equal(list.items[0].href, "https://example.com/one");
  assert.deepEqual(list.items[400], { note: "…truncated" });
});

test("form controls carry properties and password values are omitted", () => {
  const fixture = {
    title: "Form", url: "https://example.com/",
    body: [{
      tag: "form", attrs: { action: "/submit", method: "POST" },
      children: [
        { tag: "input", attrs: { type: "text", name: "user", placeholder: "User", value: "alice" } },
        { tag: "input", attrs: { type: "password", name: "pw", value: "hunter2" } },
        { tag: "button", text: "Go" },
      ],
    }],
  };
  const digest = runReadPage(fixture);
  const form = digest.body[0];
  assert.equal(form.tag, "form");
  assert.equal(form.action, "https://example.com/submit");
  assert.equal(form.method, "post");
  const [user, pw, button] = form.children;
  assert.equal(user.name, "user");
  assert.equal(user.placeholder, "User");
  assert.equal(user.value, "alice");
  assert.equal(pw.type, "password");
  assert.equal(pw.value, undefined);
  assert.equal(button.text, "Go");
});

test("node budget appends exactly one body-level marker", () => {
  /** @type {any[]} */
  const body = [];
  for (let index = 0; index < 8100; index++) {
    body.push({ tag: "p", text: `p${index}` });
  }
  const fixture = { title: "Budget", url: "https://example.com/", body };
  const digest = runReadPage(fixture);
  const markers = digest.body.filter((/** @type {any} */ node) => node.note === "truncated: node budget reached");
  assert.equal(markers.length, 1);
  assert.equal(digest.body[digest.body.length - 1].note, "truncated: node budget reached");
  assert.ok(digest.body.length < 8100);
});
