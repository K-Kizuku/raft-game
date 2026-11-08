// E2E テスト（T-506）: タイトル → ステージ選択 → 紹介 → 出航 → 航行 → エンジニアモード。
// 開発のサーバー（Go の standalone・モックなし・Turnstile なし）に対して、1 つのページで画面を順にたどり、
// 各画面のスクリーンショットを e2e/artifacts/ に保存する。
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";

import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

import { ARTIFACTS_DIR, findBrowser } from "./env.ts";

const STAGE1_TITLE = "はじめての郵便船";
const ISLANDS = 3;
const NODES = 3;

const browser = findBrowser();

test.describe.configure({ mode: "serial" });

test.describe("1 ラウンドを通す", () => {
  test.skip(!browser.found, browser.found ? "" : browser.reason);

  let page: Page;

  test.beforeAll(async ({ browser: b }) => {
    mkdirSync(ARTIFACTS_DIR, { recursive: true });
    page = await b.newPage();
  });

  test.afterAll(async () => {
    await page?.close();
  });

  async function shot(name: string): Promise<void> {
    await page.screenshot({ path: resolve(ARTIFACTS_DIR, `${name}.png`) });
  }

  test("1. タイトルが出て「遊ぶ」でステージ選択に行ける", async () => {
    await page.goto("/");
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByRole("link", { name: /遊ぶ/ })).toBeVisible();
    await shot("01-title");

    await page.getByRole("link", { name: /遊ぶ/ }).click();
    await expect(page.getByRole("button", { name: `${STAGE1_TITLE}を選ぶ` })).toBeEnabled();
    await shot("02-stage-select");
  });

  test("2. Stage 1 を選んで出航するとゲーム画面になり、時間が動き、島と郵便局長の通知が出る", async () => {
    await page.getByRole("button", { name: `${STAGE1_TITLE}を選ぶ` }).click();
    const depart = page.getByRole("button", { name: "出航する" });
    await expect(depart).toBeEnabled();
    await shot("03-intro");

    await depart.click();
    const hud = page.getByRole("region", { name: "状況" });
    const time = hud.locator(".hud-time");
    await expect(time).toBeVisible();

    // チュートリアルは吹き出しの間ゲームを止めるので、終えてから時間が動くのを確かめる。
    const finish = page
      .getByRole("complementary", { name: "チュートリアル" })
      .getByRole("button", { name: "チュートリアルを終える" });
    await expect(finish).toBeVisible();
    await finish.click();
    await expect(finish).toBeHidden();

    const before = (await time.textContent()) ?? "";
    await expect
      .poll(async () => (await time.textContent()) ?? "", { timeout: 20_000 })
      .not.toBe(before);

    const map = page.getByRole("region", { name: "マップ" });
    await expect(map.getByRole("navigation", { name: "島を選ぶ" }).getByRole("button")).toHaveCount(
      ISLANDS,
    );

    const notices = map.getByRole("region", { name: "お知らせ" }).locator(".notice");
    await expect.poll(() => notices.count(), { timeout: 30_000 }).toBeGreaterThanOrEqual(1);
    await shot("04-game");
  });

  test("3. 船を選び、手紙を積み、出航できる", async () => {
    const panel = page.getByRole("region", { name: "フェリー操作" });
    const ships = panel.getByRole("group", { name: "船を選ぶ" }).getByRole("button");
    await expect(ships.first()).toBeVisible();
    await ships.first().click();
    await expect(ships.first()).toHaveAttribute("aria-pressed", "true");

    const detail = panel.locator(".ferry-panel__detail");
    await expect(detail).toBeVisible();

    // 港の手紙を 1 通積む（ボタンを押すと積み込みのコマンドを送る）。
    const portLetter = detail.locator(".ferry-panel__port button[data-letter-id]").first();
    await expect(portLetter).toBeVisible({ timeout: 30_000 });
    await expect(portLetter).not.toHaveAttribute("aria-disabled", "true", { timeout: 30_000 });
    await portLetter.click();
    await expect(detail.locator(".ferry-panel__cargo button[data-letter-id]")).not.toHaveCount(0);
    await shot("05-ferry-loaded");

    // 行き先を選ぶと出航できるようになる。
    const destination = detail.getByRole("group", { name: "行き先" }).getByRole("radio").first();
    await destination.check();
    await expect(destination).toBeChecked();

    const departButton = detail.getByRole("button", { name: "出航", exact: true });
    await expect(departButton).not.toHaveAttribute("aria-disabled", "true", { timeout: 30_000 });
    await departButton.click();
    await expect(detail.getByText("航行中は操作できません。")).toBeVisible();
    await shot("06-ferry-departed");
  });

  test("4. E キーでエンジニアモードが開き、ノード一覧に 3 行出る", async () => {
    // フォーカスが入力欄にあると E キーを拾わないので、外してから押す。
    await page.locator("body").focus();
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    });
    await page.keyboard.press("e");
    const engineer = page.getByRole("complementary", { name: "エンジニアパネル" });
    await expect(engineer).toBeVisible();
    await expect(
      engineer.getByRole("table", { name: "ノード一覧" }).locator("tbody tr"),
    ).toHaveCount(NODES);
    await shot("07-engineer");
  });
});
