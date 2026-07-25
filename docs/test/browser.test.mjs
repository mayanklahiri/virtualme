import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { chromium } from "@playwright/test";
import test from "node:test";

const origin = "http://127.0.0.1:41730";
const routes = ["", "guide/", "architecture/", "configuration/", "blog/", "blog/welcome/", "about/", "no-more-bills/", "404.html"];
let server;
function contrast(a, b) {
  const luminance = (hex) => {
    if (/^#[0-9a-f]{3}$/i.test(hex)) hex = `#${[...hex.slice(1)].map((value) => value + value).join("")}`;
    const values = hex.match(/[0-9a-f]{2}/gi).map((value) => Number.parseInt(value, 16) / 255)
      .map((value) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
    return values[0] * 0.2126 + values[1] * 0.7152 + values[2] * 0.0722;
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
      page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
      page.on("pageerror", (error) => errors.push(error.message));
      page.on("requestfailed", (request) => failed.push(request.url()));
      for (const route of routes) {
        const response = await page.goto(`${origin}/virtualme/${route}`);
        assert.equal(response.status(), 200, route);
        assert.equal(await page.locator("main").count(), 1, route);
        assert.equal(await page.locator("h1").count(), 1, route);
        assert.match(await page.locator("footer").innerText(), /© 2026 Mayank Lahiri/, route);
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), route);
      }
      assert.deepEqual(errors, []);
      assert.deepEqual(failed, []);
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
    const page = await browser.newPage({ viewport: { width: 375, height: 812 } });
    await page.goto(`${origin}/virtualme/`);
    await page.keyboard.press("Tab");
    assert.equal(await page.evaluate(() => document.activeElement?.className), "skip-link");
    await page.keyboard.press("Enter");
    assert.equal(await page.evaluate(() => document.activeElement?.id), "main");
    const menu = page.getByRole("button", { name: "Menu" });
    await menu.click();
    assert.equal(await menu.getAttribute("aria-expanded"), "true");
    await page.keyboard.press("Escape");
    assert.equal(await menu.getAttribute("aria-expanded"), "false");
    assert.equal(await menu.evaluate((element) => element === document.activeElement), true);
    const shake = page.getByRole("button", { name: "Gently shake" });
    await shake.click();
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
      }
    }
  } finally {
    await browser.close();
  }
});

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
    assert.equal(await page.getByRole("navigation").getByText("About").count(), 0);
    assert.equal(await page.getByRole("navigation").getByText("No more bills").count(), 0);
    assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).scrollBehavior), "auto");
  } finally {
    await browser.close();
  }
});
