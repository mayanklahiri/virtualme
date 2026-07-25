import assert from "node:assert/strict";
import test from "node:test";

import { downsample, MAX_BARS } from "../controller/web/static/js/chart-data.js";

/** @param {number} count */
function makeSamples(count) {
  const samples = [];
  for (let index = 0; index < count; index++) {
    samples.push({
      ts: 1_000_000 + index * 2000,
      procCPU: [10, 20],
      gpuUtil: 50,
      tokIn: 3,
    });
  }
  return samples;
}

const modes = { tokIn: "sum" };

test("small series pass through untouched", () => {
  const samples = makeSamples(30);
  const result = downsample(samples, 2, MAX_BARS, modes);
  assert.equal(result.samples, samples);
  assert.equal(result.resSec, 2);
});

test("long series merge down to at most 36 drawn buckets", () => {
  for (const count of [73, 240, 1800, 2880]) {
    const result = downsample(makeSamples(count), 2, MAX_BARS, modes);
    assert.ok(result.samples.length <= MAX_BARS, `${count} -> ${result.samples.length}`);
    assert.ok(result.samples.length > 0);
  }
});

test("gauges average and counters sum inside each merged bucket", () => {
  const result = downsample(makeSamples(240), 2, MAX_BARS, modes);
  const k = Math.ceil(240 / MAX_BARS);
  assert.equal(result.resSec, 2 * k);
  const bucket = result.samples[0];
  assert.equal(bucket.gpuUtil, 50);
  assert.deepEqual(bucket.procCPU, [10, 20]);
  assert.equal(bucket.tokIn, 3 * k);
  // Bucket timestamp is the first merged sample's timestamp.
  assert.equal(bucket.ts, 1_000_000);
});

test("ragged array fields are padded, never dropped", () => {
  const samples = [
    { ts: 1, procCPU: [10] },
    { ts: 2, procCPU: [20, 40] },
  ];
  const result = downsample(samples, 2, 1, {});
  assert.deepEqual(result.samples[0].procCPU, [15, 20]);
});
