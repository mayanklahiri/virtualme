import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://mayanklahiri.github.io",
  base: "/virtualme",
  output: "static",
  trailingSlash: "always",
  build: { assets: "_astro" },
});
