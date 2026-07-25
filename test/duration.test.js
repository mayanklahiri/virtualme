import assert from "node:assert/strict";
import test from "node:test";

import { formatShortDuration, durationParts } from "../controller/web/static/js/duration.js";

test("formatShortDuration renders the top two nonzero units", () => {
  assert.equal(formatShortDuration(0), "0s");
  assert.equal(formatShortDuration(500), "0.5s");
  assert.equal(formatShortDuration(45_000), "45s");
  assert.equal(formatShortDuration(93_000), "1m 33s");
  assert.equal(formatShortDuration(7_380_000), "2h 3m");
  assert.equal(formatShortDuration(90_061_000), "1d 1h");
  assert.equal(formatShortDuration(3_600_000), "1h");
  assert.equal(formatShortDuration(86_400_000), "1d");
});

test("formatShortDuration is defensive about junk input", () => {
  assert.equal(formatShortDuration(-5), "0s");
  assert.equal(formatShortDuration(Number.NaN), "0s");
  assert.equal(formatShortDuration(20), "0s");
});

test("durationParts tags each rendered unit for graded styling", () => {
  assert.deepEqual(durationParts(93_000), [
    { unit: "m", text: "1m" },
    { unit: "s", text: "33s" },
  ]);
  assert.deepEqual(durationParts(90_061_000), [
    { unit: "d", text: "1d" },
    { unit: "h", text: "1h" },
  ]);
  assert.deepEqual(durationParts(0), [{ unit: "s", text: "0s" }]);
  assert.deepEqual(durationParts(500), [{ unit: "s", text: "0.5s" }]);
});
