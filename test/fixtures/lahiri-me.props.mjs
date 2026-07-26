// @ts-nocheck
import {
  count, forAll, noTruncationMarkers, yamlUnder,
} from "../helpers/digest-props.mjs";

export default [
  count({ tag: "h1" }, { eq: 1 }),
  count({ href: /^https:\/\// }, { gte: 2 }),
  forAll({ tag: "h1" }, ["text"]),
  forAll({ href: /^https:\/\// }, ["href", "text"]),
  noTruncationMarkers(),
  yamlUnder(64000),
];
