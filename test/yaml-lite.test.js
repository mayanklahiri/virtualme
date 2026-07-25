import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { parseYamlLite } from "../controller/web/static/js/yaml-lite.js";

const fixtures = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "fixtures");

const sample = `title: "Example Domain"
url: "https://example.com/"
head:
  lang: "en"
body:
  - tag: "h1"
    sel: "body > div:nth-of-type(1) > h1:nth-of-type(1)"
    text: "Example Domain"
  - tag: "a"
    sel: "body > a:nth-of-type(1)"
    href: "https://www.iana.org/domains/example"
    text: "More information..."
`;

test("parseYamlLite parses read_page subset", () => {
  const parsed = parseYamlLite(sample);
  assert.equal(parsed.title, "Example Domain");
  assert.equal(parsed.url, "https://example.com/");
  assert.equal(parsed.head.lang, "en");
  assert.equal(parsed.body.length, 2);
  assert.equal(parsed.body[0].tag, "h1");
  assert.equal(parsed.body[1].href, "https://www.iana.org/domains/example");
});

test("parseYamlLite rejects tabs and unquoted scalars", () => {
  assert.throws(() => parseYamlLite("title: Example\n"), /double-quoted strings/);
  assert.throws(() => parseYamlLite("title:\t\"x\"\n"), /tabs/);
});

test("parseYamlLite round-trips the Go emitter golden fixture", () => {
  const text = readFileSync(path.join(fixtures, "readpage-digest.golden.yaml"), "utf8");
  const parsed = parseYamlLite(text);
  assert.deepEqual(parsed, {
    title: 'Golden "Digest"\nLine two',
    url: "https://example.com/golden",
    head: {
      lang: "en",
      og: { image: "https://example.com/i.png", title: "OG" },
    },
    body: [
      {
        tag: "h1",
        sel: "body > h1:nth-of-type(1)",
        text: "Héading ünïcode",
      },
      {
        tag: "table",
        sel: "body > table:nth-of-type(1)",
        rows: [["A", "B"], ["1", ""]],
      },
      {
        tag: "ul",
        sel: "body > ul:nth-of-type(1)",
        items: [{ text: "Item", href: "https://example.com/item" }],
      },
      {
        tag: "p",
        sel: "body > p:nth-of-type(1)",
        text: "para",
        children: [{
          tag: "a",
          sel: "body > p:nth-of-type(1) > a:nth-of-type(1)",
          text: "link",
          href: "https://example.com/a",
        }],
      },
    ],
  });
});
