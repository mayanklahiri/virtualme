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
\tlang: "en"
body:
\t- tag: "h1"
\t\ttext: "Example Domain"
\t- tag: "a"
\t\thref: "https://www.iana.org/domains/example"
\t\ttext: "More information..."
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

test("parseYamlLite rejects space indentation, stray tabs, and unquoted scalars", () => {
  assert.throws(() => parseYamlLite("title: Example\n"), /double-quoted strings/);
  assert.throws(() => parseYamlLite("title:\t\"x\"\n"), /tabs are only allowed as indentation/);
  assert.throws(() => parseYamlLite('head:\n  lang: "en"\n'), /space indentation/);
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
        text: "Héading ünïcode",
      },
      {
        tag: "table",
        rows: [["A", "B"], ["1", ""]],
      },
      {
        tag: "ul",
        items: [{ text: "Item", href: "https://example.com/item" }],
      },
      {
        tag: "p",
        text: "para",
        children: [{
          tag: "a",
          text: "link",
          href: "https://example.com/a",
        }],
      },
    ],
  });
});
