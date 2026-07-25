import assert from "node:assert/strict";
import test from "node:test";
import { parseYamlLite } from "../controller/web/static/js/yaml-lite.js";

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
