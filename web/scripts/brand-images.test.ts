// favicon と OGP（T-430 / #354）。web/index.html の meta・link と、web/public/ の画像を確かめる。
// 画像は scripts/render-brand-images.sh で web/brand/ から作る（このテストは作り直さない）。
import { readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const WEB = fileURLToPath(new URL("..", import.meta.url));
const PUBLIC = join(WEB, "public");
const DESCRIPTION =
  "Raft（分散合意）を、嵐の離島をつなぐ郵便船のゲームで遊んで学ぶ。実際の離島のオープンデータを使っています。";

type Tag = { name: string; attrs: Record<string, string> };

/** index.html の <meta>・<link> を、属性の表にして返す（属性の値は "…" で書く前提） */
function headTags(html: string): Tag[] {
  const tags: Tag[] = [];
  for (const m of html.matchAll(/<(meta|link)\b([^>]*)>/g)) {
    const attrs: Record<string, string> = {};
    for (const a of (m[2] ?? "").matchAll(/([\w:-]+)="([^"]*)"/g)) {
      attrs[a[1] ?? ""] = a[2] ?? "";
    }
    tags.push({ name: m[1] ?? "", attrs });
  }
  return tags;
}

const tags = headTags(readFileSync(join(WEB, "index.html"), "utf8"));

function meta(key: "name" | "property", value: string): string | undefined {
  return tags.find((t) => t.name === "meta" && t.attrs[key] === value)?.attrs.content;
}

function link(rel: string, type?: string): Record<string, string> | undefined {
  return tags.find(
    (t) =>
      t.name === "link" && t.attrs.rel === rel && (type === undefined || t.attrs.type === type),
  )?.attrs;
}

/** PNG の幅と高さ（IHDR） */
function pngSize(file: string): { width: number; height: number } {
  const buf = readFileSync(file);
  expect(buf.subarray(0, 8).toString("hex")).toBe("89504e470d0a1a0a");
  expect(buf.subarray(12, 16).toString("ascii")).toBe("IHDR");
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
}

describe("index.html の favicon", () => {
  it("SVG・PNG の icon と apple-touch-icon を指す", () => {
    expect(link("icon", "image/svg+xml")?.href).toBe("/favicon.svg");
    expect(link("icon", "image/png")).toMatchObject({ href: "/favicon-32.png", sizes: "32x32" });
    expect(link("apple-touch-icon")).toMatchObject({
      href: "/apple-touch-icon.png",
      sizes: "180x180",
    });
  });
});

describe("index.html の OGP と description", () => {
  it("description・og:*・twitter:card がある", () => {
    expect(meta("name", "description")).toBe(DESCRIPTION);
    expect(meta("property", "og:title")).toBe("合意、乗船中。");
    expect(meta("property", "og:description")).toBe(DESCRIPTION);
    expect(meta("property", "og:type")).toBe("website");
    expect(meta("property", "og:locale")).toBe("ja_JP");
    expect(meta("name", "twitter:card")).toBe("summary_large_image");
  });

  it("og:image は一般公開のホストの絶対 URL で、1200×630", () => {
    expect(meta("property", "og:image")).toBe("https://raft.kizuku-hackathon.work/og-image.png");
    expect(meta("property", "og:image:width")).toBe("1200");
    expect(meta("property", "og:image:height")).toBe("630");
  });
});

describe("web/public の画像", () => {
  it.each([
    ["favicon-32.png", 32, 32, 20 * 1024],
    ["apple-touch-icon.png", 180, 180, 20 * 1024],
    ["og-image.png", 1200, 630, 300 * 1024],
  ])("%s は %i×%i で、大きさの目安以下", (name, width, height, maxBytes) => {
    const file = join(PUBLIC, name);
    expect(pngSize(file)).toEqual({ width, height });
    expect(statSync(file).size).toBeLessThanOrEqual(maxBytes);
  });

  it("favicon.svg の色は、トークン（tokens.css）の値だけ", () => {
    const svg = readFileSync(join(PUBLIC, "favicon.svg"), "utf8");
    const css = readFileSync(join(WEB, "src/ui/theme/tokens.css"), "utf8");
    const tokenColors = new Set(
      [...css.matchAll(/#[0-9a-f]{6}\b/gi)].map((m) => m[0].toLowerCase()),
    );
    // コメントの中の説明の色も含めて、すべての 16 進の色を調べる
    const used = [...svg.matchAll(/#[0-9a-f]{3,8}\b/gi)].map((m) => m[0].toLowerCase());
    expect(used.length).toBeGreaterThan(0);
    for (const color of used) {
      expect(tokenColors, color).toContain(color);
    }
  });
});
