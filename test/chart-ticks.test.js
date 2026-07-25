import assert from "node:assert/strict";
import test from "node:test";

const chartModule = new URL("../controller/web/static/js/chart.js", import.meta.url);
const { barTimestampBounds, chooseTicks, nearestSampleIndex } = await import(chartModule.href);

const MINUTE = 60_000;
const LOOKBACKS = {
  "15m": { span: 15 * MINUTE, steps: [5] },
  "1h": { span: 60 * MINUTE, steps: [15] },
  "3h": { span: 3 * 60 * MINUTE, steps: [30, 60] },
  "12h": { span: 12 * 60 * MINUTE, steps: [120, 180] },
  "1d": { span: 24 * 60 * MINUTE, steps: [240, 360] },
  "3d": { span: 3 * 24 * 60 * MINUTE, steps: [720, 1440] },
  "7d": { span: 7 * 24 * 60 * MINUTE, steps: [1440, 2880] },
  "30d": { span: 30 * 24 * 60 * MINUTE, steps: [10080, 14400] },
};

test("chart ticks are bounded and aligned in local time", () => {
  const lastTs = Date.UTC(2026, 6, 23, 22, 27, 31);
  const timezoneOffsetMinutes = 420;
  for (const [lookback, definition] of Object.entries(LOOKBACKS)) {
    const firstTs = lastTs - definition.span;
    for (const [width, bound] of [[375, 3], [900, 5], [1600, 5]]) {
      const { stepMs, ticks } = /** @type {{stepMs: number, ticks: number[]}} */ (
        chooseTicks(firstTs, lastTs, width, lookback, timezoneOffsetMinutes)
      );
      assert.ok(definition.steps.includes(stepMs / MINUTE), `${lookback}/${width}: valid candidate`);
      assert.ok(ticks.length <= bound, `${lookback}/${width}: ${ticks.length} <= ${bound}`);
      assert.ok(ticks.every((tick) => tick > firstTs && tick < lastTs), `${lookback}/${width}: in range`);
      assert.ok(
        ticks.every((tick) => (tick - timezoneOffsetMinutes * MINUTE) % stepMs === 0),
        `${lookback}/${width}: local boundary`,
      );
    }
  }
});

test("chart ticks choose the smallest candidate satisfying the width bound", () => {
  const lastTs = Date.UTC(2026, 6, 23, 22, 27, 31);
  assert.equal(chooseTicks(lastTs - LOOKBACKS["30d"].span, lastTs, 900, "30d", 420).stepMs, 7 * 24 * 60 * MINUTE);
  assert.equal(chooseTicks(lastTs - LOOKBACKS["30d"].span, lastTs, 375, "30d", 420).stepMs, 10 * 24 * 60 * MINUTE);
});

test("short data spans fall back to finer steps so the axis keeps labels", () => {
  // A freshly restarted instance may only have a few minutes of data; the
  // preferred 15m step for the 1h window would then produce zero ticks.
  const lastTs = Date.UTC(2026, 6, 23, 22, 27, 31);
  const { stepMs, ticks } = /** @type {{stepMs: number, ticks: number[]}} */ (
    chooseTicks(lastTs - 4 * MINUTE, lastTs, 900, "1h", 420)
  );
  assert.ok(ticks.length >= 2, `fallback produced ${ticks.length} ticks`);
  assert.ok(stepMs < 15 * MINUTE, `fallback step ${stepMs} is finer than 15m`);
  assert.ok(ticks.every((tick) => (tick - 420 * MINUTE) % stepMs === 0), "local boundary");
});

test("a trailing bucket before a synthetic gap keeps server resolution width", () => {
  // Regression: gap-splitting must not widen the last sample toward the next segment.
  const beforeGap = [{ ts: 10_000 }, { ts: 12_000 }];
  const afterGap = [{ ts: 132_000 }];
  assert.deepEqual(barTimestampBounds(beforeGap, 1, 2), { leftTs: 11_000, rightTs: 13_000 });
  assert.deepEqual(barTimestampBounds(afterGap, 0, 2), { leftTs: 131_000, rightTs: 133_000 });
});

test("hover hit-testing selects the nearest timestamp across a gap", () => {
  const samples = [{ ts: 10_000 }, { ts: 12_000 }, { ts: 132_000 }];
  assert.equal(nearestSampleIndex(samples, 60_000), 1);
  assert.equal(nearestSampleIndex(samples, 100_000), 2);
});

test("status markup carries split GPU charts and the new LLM/action charts", async () => {
  const { readFile } = await import("node:fs/promises");
  const html = await readFile(new URL("../controller/web/static/index.html", import.meta.url), "utf8");
  for (const id of ["chart-gpu-util", "chart-gpu-mem", "chart-tokens", "chart-throughput", "chart-actions"]) {
    assert.ok(html.includes(`id="${id}"`), `index.html has #${id}`);
  }
  assert.ok(!html.includes('id="chart-gpu"'), "combined GPU chart removed");
  assert.ok(html.includes("Quick Options"), "Quick Options panel present");
  assert.ok(html.includes('id="scheduler-switch"'), "scheduler pause switch present");
  // Cockpit layout: fixed square lamp buttons with labels beneath and
  // tooltips instead of the old knob switches and "?" hints.
  assert.equal((html.match(/class="qo-btn"/g) ?? []).length, 2, "two cockpit buttons");
  assert.equal((html.match(/class="qo-tip" role="tooltip"/g) ?? []).length, 2, "two tooltips");
  assert.ok(!html.includes("qo-hint"), "old hint buttons removed");
  assert.ok(!html.includes("qo-row"), "old switch rows removed");
  // CPU and memory share one .chart-row ancestor (desktop side-by-side).
  const cpuAt = html.indexOf('id="chart-cpu"');
  const memAt = html.indexOf('id="chart-mem"');
  const rowOpen = html.lastIndexOf('<div class="chart-row"', cpuAt);
  const rowClose = html.indexOf("</div>", memAt);
  assert.ok(rowOpen >= 0 && rowClose > memAt && cpuAt < memAt,
    "chart-cpu and chart-mem share one chart-row");
  assert.ok(!html.slice(rowOpen, rowClose).includes('id="gpu-charts"'),
    "CPU/MEM row is not the GPU row");
});
