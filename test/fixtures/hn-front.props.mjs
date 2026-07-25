// @ts-nocheck
import assert from "node:assert/strict";
import {
  count, custom, noTruncationMarkers, textMatches, walkNodes, yamlUnder,
} from "../helpers/digest-props.mjs";

function descendants(node) {
  return walkNodes(node.children ?? []);
}

export default [
  count({ tag: "span", text: /^\d+\.$/ }, { eq: 30 }),
  count({ tag: "a", href: /^https:\/\/news\.ycombinator\.com\/vote\?id=/ }, { gte: 29 }),
  count({ tag: "a", href: /^https:\/\/news\.ycombinator\.com\/item\?id=/ }, { gte: 25 }),
  textMatches(/^\d+ points?$/, { gte: 29 }),
  textMatches(/^(?:\d+ comments?|discuss)$/, { gte: 29 }),
  count({ tag: "a", text: "Hacker News" }, { eq: 1 }),
  custom("all 30 stories group title, URL, score, and comment metadata", ({ nodes }) => {
    const stories = nodes.filter((node) => node.tag === "article");
    assert.equal(stories.length, 30);
    let scored = 0;
    let discussed = 0;
    for (const story of stories) {
      const children = descendants(story);
      assert.equal(typeof story.rank, "number", JSON.stringify(story));
      assert.ok(story.title, JSON.stringify(story));
      assert.doesNotMatch(story.title, /…$/);
      assert.match(story.url, /^https?:\/\//);
      assert.match(story.comment_url, /^https:\/\/news\.ycombinator\.com\/item\?id=\d+/);
      assert.equal(story.title_link, `[${story.title}](${story.comment_url})`);
      if (/^\d+ points?$/.test(story.score ?? "")) scored++;
      if (/^(?:\d+ comments?|discuss)$/.test(story.comments ?? "")) discussed++;
      assert.ok(children.some((node) => /\/item\?id=\d+/.test(node.href ?? "")), JSON.stringify(story));
    }
    assert.ok(scored >= 29, `${scored} scored stories`);
    assert.ok(discussed >= 29, `${discussed} discussed stories`);
  }),
  noTruncationMarkers(),
  yamlUnder(64000),
];
