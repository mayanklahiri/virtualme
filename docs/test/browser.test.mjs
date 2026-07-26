import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { chromium } from "@playwright/test";
import test from "node:test";

const origin = "http://127.0.0.1:41730";
const routes = ["", "guide/", "architecture/", "configuration/", "blog/", "blog/welcome/", "about/", "no-more-bills/", "404.html"];
let server;
function contrast(a, b) {
  const luminance = (color) => {
    if (/^#[0-9a-f]{3}$/i.test(color)) color = `#${[...color.slice(1)].map((value) => value + value).join("")}`;
    const values = color.startsWith("#")
      ? color.match(/[0-9a-f]{2}/gi).map((value) => Number.parseInt(value, 16) / 255)
      : color.match(/[\d.]+/g).slice(0, 3).map((value) => Number(value) / 255);
    const linear = values
      .map((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
    return linear[0] * 0.2126 + linear[1] * 0.7152 + linear[2] * 0.0722;
  };
  const [bright, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (bright + 0.05) / (dark + 0.05);
}

test.before(async () => {
  server = spawn(process.execPath, ["test/helpers.mjs"], { stdio: ["ignore", "pipe", "inherit"] });
  await new Promise((resolve, reject) => {
    server.stdout.once("data", resolve);
    server.once("error", reject);
    server.once("exit", (code) => reject(new Error(`server exited ${code}`)));
  });
});

test.after(() => server?.kill());

test("all routes render accessibly at desktop and mobile widths", async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const viewport of [{ width: 1440, height: 900 }, { width: 375, height: 812 }]) {
      const page = await browser.newPage({ viewport });
      const errors = [];
      const failed = [];
      const wrongBase = [];
      page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
      page.on("pageerror", (error) => errors.push(error.message));
      page.on("requestfailed", (request) => failed.push(request.url()));
      page.on("request", (request) => {
        const requested = new URL(request.url());
        if (requested.origin === origin && !requested.pathname.startsWith("/virtualme/")) wrongBase.push(request.url());
        if (requested.protocol === "http:" && requested.origin !== origin) wrongBase.push(request.url());
      });
      for (const route of routes) {
        const response = await page.goto(`${origin}/virtualme/${route}`);
        assert.equal(response.status(), 200, route);
        assert.equal(await page.locator("main").count(), 1, route);
        assert.equal(await page.locator("h1").count(), 1, route);
        assert.match(await page.locator("footer").innerText(), /© 2026 Mayank Lahiri/, route);
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), route);
        await page.screenshot();
        const smallTargets = await page.locator("button,input,select,.button,.permalink,nav a").evaluateAll((elements) => elements
          .filter((element) => {
            const style = getComputedStyle(element);
            const box = element.getBoundingClientRect();
            return style.visibility !== "hidden" && style.display !== "none" && box.width > 0 && box.height > 0;
          })
          .map((element) => {
            const box = element.getBoundingClientRect();
            return { text: element.getAttribute("aria-label") || element.textContent?.trim(), width: box.width, height: box.height };
          })
          .filter(({ width, height }) => width < 44 || height < 44));
        assert.deepEqual(smallTargets, [], `${route}: targets smaller than 44px`);
        await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), `${route}: overflow at 200% text zoom`);
        await page.evaluate(() => { document.documentElement.style.fontSize = ""; });
      }
      assert.deepEqual(errors, []);
      assert.deepEqual(failed, []);
      assert.deepEqual(wrongBase, []);
      await page.close();
    }
    for (const viewport of [{ width: 320, height: 700 }, { width: 1920, height: 1080 }]) {
      const page = await browser.newPage({ viewport });
      await page.goto(`${origin}/virtualme/`);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth));
      await page.screenshot();
      await page.close();
    }
  } finally {
    await browser.close();
  }
});

test("keyboard navigation and interactive memories preserve focus", async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    await page.addInitScript(() => {
      globalThis.__copiedText = "";
      Object.defineProperty(globalThis.navigator, "clipboard", { configurable: true, value: { writeText: async (text) => { globalThis.__copiedText = text; } } });
    });
    await page.goto(`${origin}/virtualme/`);
    await page.keyboard.press("Tab");
    assert.equal(await page.evaluate(() => document.activeElement?.className), "skip-link");
    await page.keyboard.press("Enter");
    assert.equal(await page.evaluate(() => document.activeElement?.id), "main");
    await page.goto(`${origin}/virtualme/`);
    const tabOrder = [];
    for (let index = 0; index < 25; index += 1) {
      await page.keyboard.press("Tab");
      tabOrder.push(await page.evaluate(() => document.activeElement?.getAttribute("aria-label") || document.activeElement?.textContent?.trim()));
    }
    const positions = ["Skip to content", "Virtual Me documentation home", "Home", "5-minute guide", "How it works", "Configuration", "Blog", "GitHub", "README", "Theme", "Start in five minutes", "See how it works"]
      .map((label) => tabOrder.findIndex((value) => value === label));
    assert.equal(positions.every((value) => value >= 0), true, tabOrder.join(" | "));
    assert.deepEqual([...positions].sort((a, b) => a - b), positions);

    await page.goto(`${origin}/virtualme/guide/`);
    const copy = page.getByRole("button", { name: "Copy command" }).first();
    for (let index = 0; index < 30 && !(await copy.evaluate((element) => element === document.activeElement)); index += 1) await page.keyboard.press("Tab");
    assert.equal(await copy.evaluate((element) => element === document.activeElement), true);
    await page.keyboard.press("Enter");
    assert.equal(await page.evaluate(() => globalThis.__copiedText), "node --version");

    await page.setViewportSize({ width: 375, height: 812 });
    await page.goto(`${origin}/virtualme/`);
    const menu = page.getByRole("button", { name: "Menu" });
    await menu.click();
    assert.equal(await menu.getAttribute("aria-expanded"), "true");
    await page.keyboard.press("Escape");
    assert.equal(await menu.getAttribute("aria-expanded"), "false");
    assert.equal(await menu.evaluate((element) => element === document.activeElement), true);
    const globe = page.locator(".globe");
    await globe.hover();
    assert.equal(await page.getByRole("link", { name: "Open the memory cabinet" }).isVisible(), true);
    await page.reload();
    await globe.focus();
    assert.equal(await page.getByRole("link", { name: "Open the memory cabinet" }).isVisible(), true);
    await page.goto(`${origin}/virtualme/about/`);
    const locality = page.getByRole("button", { name: "Open Locality memory" });
    await locality.click();
    assert.equal(await page.evaluate(() => document.activeElement?.id), "principle-0");
    await page.keyboard.press("Escape");
    assert.equal(await locality.evaluate((element) => element === document.activeElement), true);
  } finally {
    await browser.close();
  }
});

test("all shared themes expose complete light and dark tokens", async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    await page.goto(`${origin}/virtualme/`);
    for (const theme of ["modern", "editorial", "terminal", "warm", "contrast", "arctic", "solar", "studio"]) {
      for (const variant of ["light", "dark"]) {
        await page.evaluate(({ theme, variant }) => {
          localStorage.setItem("vm-theme", theme);
          localStorage.setItem("vm-variant", variant);
        }, { theme, variant });
        await page.reload();
        const values = await page.evaluate(() => {
          const style = getComputedStyle(document.documentElement);
          return Object.fromEntries(["--bg", "--surface", "--fg", "--muted", "--accent", "--accent-fg", "--ok", "--err", "--border", "--brand-a", "--brand-b", "--font-heading", "--font-body", "--font-mono", "--p1", "--p2", "--p3", "--p4", "--p5", "--p6", "--p7", "--p8"].map((name) => [name, style.getPropertyValue(name).trim()]));
        });
        assert.equal(Object.values(values).every(Boolean), true, `${theme}/${variant}`);
        assert.ok(contrast(values["--fg"], values["--bg"]) >= 4.5, `${theme}/${variant} body contrast`);
        assert.ok(contrast(values["--fg"], values["--surface"]) >= 4.5, `${theme}/${variant} control contrast`);
        assert.ok(contrast(values["--accent-fg"], values["--accent"]) >= 4.5, `${theme}/${variant} accent control contrast`);
      }
    }
  } finally {
    await browser.close();
  }
});

test("configuration filtering, navigation, facts, and deep-link focus work", async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    await page.goto(`${origin}/virtualme/configuration/#llama-context-tokens`);
    assert.equal(await page.evaluate(() => document.activeElement?.id), "llama-context-tokens");
    const card = page.locator("#llama-context-tokens");
    await expectText(card, ["Allowed values", "Constraints", "Secret", "Tradeoffs", "Open this option in the local console"]);
    await page.locator("#mail-smarthost-password").scrollIntoViewIfNeeded();
    assert.match(await page.locator("#mail-smarthost-password").innerText(), /Secret reference policy/);
    assert.match(await page.locator("#integrations-telegram-bot-token").innerText(), /Resolved when/);
    assert.equal(await page.getByRole("link", { name: "BotFather" }).count(), 1);
    const filter = page.locator("#config-filter");
    await filter.fill("llama.contexttokens");
    assert.match(await page.locator("#config-count").innerText(), /^1 matching options$/);
    assert.equal(new URL(page.url()).searchParams.get("q"), "llama.contexttokens");
    assert.equal(await page.locator("[data-config-card]:not([hidden])").count(), 1);
    await filter.fill("");
    assert.equal(new URL(page.url()).searchParams.has("q"), false);
    assert.equal(await page.locator("#config-count").innerText(), "");
    await page.locator("#integrations").scrollIntoViewIfNeeded();
    await page.waitForFunction(() => document.querySelector('[data-section-link="integrations"]')?.getAttribute("aria-current") === "true");
    await page.setViewportSize({ width: 375, height: 812 });
    assert.equal(await page.locator("#config-section-picker").isVisible(), true);
    await page.locator("#config-section-picker").selectOption("llama");
    assert.equal(await page.evaluate(() => document.activeElement?.id), "llama");
  } finally {
    await browser.close();
  }
});

async function expectText(locator, values) {
  const text = await locator.innerText();
  for (const value of values) assert.match(text, new RegExp(value));
}

test("navigation, theme persistence, hidden routes, and reduced motion work", async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 375, height: 812 }, reducedMotion: "reduce" });
    await page.goto(`${origin}/virtualme/`);
    await page.getByRole("button", { name: /theme/i }).click();
    await page.getByRole("button", { name: "Editorial" }).click();
    await page.getByRole("button", { name: /theme/i }).click();
    await page.getByRole("button", { name: "Dark" }).click();
    await page.reload();
    assert.equal(await page.locator("html").getAttribute("data-theme"), "editorial");
    assert.equal(await page.locator("html").getAttribute("data-variant"), "dark");
    await page.getByRole("button", { name: /theme/i }).click();
    assert.equal(await page.getByRole("button", { name: "Editorial" }).getAttribute("aria-pressed"), "true");
    assert.equal(await page.getByRole("button", { name: "Dark" }).getAttribute("aria-pressed"), "true");
    assert.equal(await page.getByRole("navigation").getByText("About").count(), 0);
    assert.equal(await page.getByRole("navigation").getByText("No more bills").count(), 0);
    assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).scrollBehavior), "auto");
    await page.getByRole("button", { name: /theme/i }).press("Escape");
    await page.getByRole("button", { name: "Gently shake" }).click();
    assert.equal(await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running").length), 0);

    await page.goto(`${origin}/virtualme/`);
    await page.getByRole("link", { name: "Start in five minutes" }).click();
    assert.equal(new URL(page.url()).pathname, "/virtualme/guide/");
    await page.goto(`${origin}/virtualme/blog/`);
    await page.getByRole("link", { name: "Welcome to Virtual Me" }).click();
    assert.equal(new URL(page.url()).pathname, "/virtualme/blog/welcome/");
    await page.goto(`${origin}/virtualme/404.html`);
    await page.getByRole("link", { name: "Home" }).last().click();
    assert.equal(new URL(page.url()).pathname, "/virtualme/");

    const noScript = await browser.newPage({ javaScriptEnabled: false });
    await noScript.goto(`${origin}/virtualme/configuration/`);
    assert.ok(await noScript.locator("[data-config-card]").count() > 20);
    await noScript.goto(`${origin}/virtualme/blog/welcome/`);
    assert.match(await noScript.locator("main").innerText(), /What runs locally/);
    await noScript.close();
  } finally {
    await browser.close();
  }
});
