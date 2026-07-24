import assert from "node:assert/strict";
import test from "node:test";

import { selectorLabel, serializeSelector } from "../controller/web/static/js/projects.js";

test("project selector labels cover groups and individual days", () => {
  assert.equal(selectorLabel("weekday morning"), "Every weekday morning");
  assert.equal(selectorLabel("fri night"), "Fridays at night");
  assert.equal(selectorLabel("mon,wed,fri"), "Mon, Wed, Fri, any time");
  assert.equal(selectorLabel("anytime"), "Every day");
});

test("project selector serialization follows scheduler grammar", () => {
  assert.equal(serializeSelector(new Set(["tue", "thu"]), "morning"), "tue,thu morning");
  assert.equal(serializeSelector(new Set(["weekday"]), "anytime"), "weekday");
  assert.equal(serializeSelector(new Set(["everyday"]), "late-night"), "late-night");
});
