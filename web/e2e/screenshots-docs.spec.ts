// Screenshots of the user guide (docs/images/guide/<lang>/*.png): the real
// server with the generated sample library, both interface languages. Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-docs.spec.ts --project visual
import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { Browser, Page } from "@playwright/test";
import { ADMIN, expect, scanLibrary, setupAdmin, test } from "./vault";

const OUT = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
  "..",
  "docs",
  "images",
  "guide",
);

test.skip(
  !process.env.SCREENSHOTS,
  "set SCREENSHOTS=1 to refresh docs/images/guide",
);
test.use({ library: "all", fakes: true, migrate: true });

const LANGS = [
  { tag: "ru", locale: "ru-RU" },
  { tag: "en", locale: "en-GB" },
] as const;

async function newPage(browser: Browser, baseURL: string, locale: string) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1,
    colorScheme: "dark",
    locale,
    reducedMotion: "reduce",
  });
  // «Язык интерфейса» picked in this browser (the profile's own wins otherwise)
  await ctx.addInitScript(
    (l) => localStorage.setItem("vault.uiLanguage", l),
    locale.slice(0, 2),
  );
  return ctx.newPage();
}

async function shot(page: Page, lang: string, name: string, height = 800) {
  // a tall page (the profile's own settings) needs a taller window
  await page.setViewportSize({ width: 1280, height });
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(500);
  // the test server's temporary folders read as the container's: its media
  // root holds a `media/` folder of its own, which a real server has not
  await page.evaluate(() => {
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      const t = n.nodeValue ?? "";
      if (t.includes("vault-e2e-")) {
        n.nodeValue = t
          .replace(/\/\S*?vault-e2e-[A-Za-z0-9]+\/media-root/g, "/media")
          .replace(/\/\S*?vault-e2e-[A-Za-z0-9]+\/import/g, "/import");
      }
      const v = n.nodeValue ?? "";
      if (/^\/?media\/media\//.test(v) || /^media\/\w+$/.test(v)) {
        n.nodeValue = v.replace(/^(\/?)media\/media\//, "$1media/").replace(/^media\/(\w+)$/, "$1");
      }
    }
  });
  mkdirSync(path.join(OUT, lang), { recursive: true });
  await page.screenshot({ path: path.join(OUT, lang, `${name}.png`) });
}

test("guide screenshots", async ({ browser, vaultURL }) => {
  test.setTimeout(300_000);

  for (const l of LANGS) {
    const page = await newPage(browser, vaultURL, l.locale);
    await page.goto("/");
    await page.waitForURL(/\/welcome$/);
    await shot(page, l.tag, "welcome");
    await page.context().close();
  }

  const admin = await newPage(browser, vaultURL, "ru-RU");
  await setupAdmin(admin.request);
  await scanLibrary(admin.request, (s) => s.movies > 0 && s.albums > 0);
  await admin.context().close();

  // [file name, address, window height]
  const pages: [string, string, number?][] = [
    ["wizard", "/setup/wizard"],
    ["my-settings", "/settings", 1250],
    ["settings-library", "/manage/settings/library"],
    ["settings-watching", "/manage/settings/watching"],
    ["settings-processing", "/manage/settings/processing", 1100],
    ["settings-mail", "/manage/settings/kindle"],
    ["settings-accounts", "/manage/settings/accounts"],
    ["settings-address", "/manage/settings/address"],
    ["my-accounts", "/settings/accounts"],
    ["channels", "/manage/channels"],
    ["health", "/manage/health", 1100],
    ["tasks", "/manage/tasks"],
    ["migrate", "/manage/migrate", 1000],
    ["pair", "/pair"],
    ["profiles", "/manage/profiles"],
  ];
  for (const l of LANGS) {
    const page = await newPage(browser, vaultURL, l.locale);
    const r = await page.request.post("/api/login", {
      data: { userId: 1, password: ADMIN.password },
    });
    expect(r.ok(), await r.text()).toBeTruthy();
    // The Mac that takes the pictures has an ffmpeg without chromaprint; the
    // official image has it. Show the state a guide reader's server is in,
    // so the pages do not open with a warning that is about this machine.
    await page.route("**/api/settings/transcoding", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      const res = await route.fetch();
      const view = await res.json();
      await route.fulfill({ response: res, json: { ...view, fingerprint: true } });
    });
    for (const [name, url, height] of pages) {
      await page.goto(url);
      await shot(page, l.tag, name, height);
    }
    const sheets: [string, string, RegExp][] = [
      ["addons-add", "/manage/addons", /^(Добавить расширение|Add Extension)$/],
      [
        "settings-mail-setup",
        "/manage/settings/kindle",
        /^(Настроить…|Set Up…)$/,
      ],
      [
        "tv-sources-add",
        "/manage/channels/sources",
        /^(Добавить источник|Add Source)$/,
      ],
    ];
    for (const [name, url, button] of sheets) {
      await page.goto(url);
      const b = page.getByRole("button", { name: button }).first();
      await b.waitFor({ timeout: 10_000 }).catch(() => {});
      if (!(await b.count())) continue;
      await b.click();
      await page
        .getByRole("dialog")
        .first()
        .waitFor({ timeout: 5_000 })
        .catch(() => {});
      await shot(page, l.tag, name);
    }
    await page.context().close();
  }
});
