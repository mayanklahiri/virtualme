// Pure chart-series helpers (no DOM) so Node unit tests can cover them.

/** Maximum number of bars any chart may draw. */
export const MAX_BARS = 120;

/**
 * Merge adjacent samples so no more than maxBars buckets remain.
 * Numeric fields average by default; fields marked "sum" in fieldModes
 * accumulate (counters). Array fields average element-wise, padding
 * ragged arrays with zeros. Bucket timestamp = first merged sample's ts.
 *
 * @param {Array<Record<string, any>>} samples
 * @param {number} resSec resolution of the input samples, in seconds
 * @param {number} [maxBars]
 * @param {Record<string, string>} [fieldModes]
 * @returns {{samples: Array<Record<string, any>>, resSec: number}}
 */
export function downsample(samples, resSec, maxBars = MAX_BARS, fieldModes = {}) {
  if (!Array.isArray(samples) || samples.length <= maxBars) {
    return { samples, resSec };
  }
  const k = Math.ceil(samples.length / maxBars);
  const merged = [];
  for (let start = 0; start < samples.length; start += k) {
    const group = samples.slice(start, start + k);
    merged.push(mergeGroup(group, fieldModes));
  }
  return { samples: merged, resSec: resSec * k };
}

/**
 * @param {Array<Record<string, any>>} group
 * @param {Record<string, string>} fieldModes
 * @returns {Record<string, any>}
 */
function mergeGroup(group, fieldModes) {
  /** @type {Record<string, any>} */
  const out = { ts: group[0].ts };
  const keys = new Set();
  for (const sample of group) {
    for (const key of Object.keys(sample)) {
      if (key !== "ts") keys.add(key);
    }
  }
  for (const key of keys) {
    if (Array.isArray(group.find((sample) => sample[key] !== undefined)?.[key])) {
      let length = 0;
      for (const sample of group) {
        length = Math.max(length, Array.isArray(sample[key]) ? sample[key].length : 0);
      }
      const totals = new Array(length).fill(0);
      for (const sample of group) {
        const values = Array.isArray(sample[key]) ? sample[key] : [];
        for (let index = 0; index < values.length; index++) {
          totals[index] += Number(values[index]) || 0;
        }
      }
      out[key] = totals.map((total) => total / group.length);
      continue;
    }
    let total = 0;
    for (const sample of group) {
      total += Number(sample[key]) || 0;
    }
    out[key] = fieldModes[key] === "sum" ? total : total / group.length;
  }
  return out;
}
