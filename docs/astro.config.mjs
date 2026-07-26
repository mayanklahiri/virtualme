import { defineConfig } from "astro/config";
import { url } from "./src/config/urls.ts";

function baseAwareMarkdownLinks() {
  const visit = (node) => {
    if (node?.type === "link" && typeof node.url === "string" && node.url.startsWith("site:")) {
      node.url = url(node.url.slice("site:".length));
    }
    if (Array.isArray(node?.children)) node.children.forEach(visit);
  };
  return visit;
}

export default defineConfig({
  site: "https://mayanklahiri.github.io",
  base: "/virtualme",
  output: "static",
  trailingSlash: "always",
  build: { assets: "_astro" },
  markdown: { remarkPlugins: [baseAwareMarkdownLinks] },
});
