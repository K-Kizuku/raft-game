// デザインの見本の画面（T-420）。開発のときだけ、URL の `?theme-preview` で開く（App.tsx）。
// 本番のビルドには入らない（App.tsx が import.meta.env.DEV のときだけ遅延読み込みする）。
import type { ReactElement } from "react";

import { tokens, type ColorTokenName } from "./tokens.ts";
import "./ThemePreview.css";

const COLOR_GROUPS: { title: string; names: ColorTokenName[] }[] = [
  {
    title: "紙と墨",
    names: ["--color-paper", "--color-paper-deep", "--color-ink", "--color-ink-muted"],
  },
  { title: "海", names: ["--color-sea-light", "--color-sea-mid", "--color-sea-deep"] },
  { title: "島", names: ["--color-land", "--color-land-green"] },
  { title: "郵便の赤・封筒の金", names: ["--color-postal", "--color-gold"] },
  { title: "嵐", names: ["--color-storm", "--color-storm-deep"] },
  { title: "状態", names: ["--color-success", "--color-warning", "--color-danger"] },
];

const FONT_SIZES: {
  label: string;
  name: "--font-size-h1" | "--font-size-h2" | "--font-size-h3" | "--font-size-body";
}[] = [
  { label: "見出し 1", name: "--font-size-h1" },
  { label: "見出し 2", name: "--font-size-h2" },
  { label: "見出し 3", name: "--font-size-h3" },
  { label: "本文", name: "--font-size-body" },
];

const SPACES = [
  "--space-1",
  "--space-2",
  "--space-3",
  "--space-4",
  "--space-5",
  "--space-6",
] as const;
const RADII = ["--radius-s", "--radius-m", "--radius-l"] as const;

export function ThemePreview(): ReactElement {
  return (
    <main className="theme-preview">
      <h1>デザインの見本</h1>
      <p className="text-muted">
        温かい絵本・海図風（docs/11-visual-design.md）。この画面は開発のときだけ開けます。
      </p>

      <section aria-labelledby="theme-preview-colors">
        <h2 id="theme-preview-colors">色</h2>
        {COLOR_GROUPS.map((group) => (
          <div key={group.title} className="theme-preview__group">
            <h3>{group.title}</h3>
            <ul className="theme-preview__swatches">
              {group.names.map((name) => (
                <li key={name} className="theme-preview__swatch">
                  <span
                    className="theme-preview__chip"
                    style={{ background: `var(${name})` }}
                    aria-hidden="true"
                  />
                  <code>{name}</code>
                  <span className="text-number">{tokens[name]}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </section>

      <section aria-labelledby="theme-preview-type">
        <h2 id="theme-preview-type">文字</h2>
        <ul className="theme-preview__type">
          {FONT_SIZES.map(({ label, name }) => (
            <li key={name} style={{ fontSize: `var(${name})` }}>
              {label}（<code>{name}</code>）: 島から島へ、手紙が届く。
            </li>
          ))}
        </ul>
        <p>
          数字（<code>.text-number</code>）:{" "}
          <span className="text-number">残り 1:23 ／ スコア 12,340</span>
        </p>
        <p className="text-muted">補足の文字（.text-muted）: 合意ができた手紙だけが確定します。</p>
        <p>
          <a href="#theme-preview-type">リンクの見本</a>
        </p>
      </section>

      <section aria-labelledby="theme-preview-buttons">
        <h2 id="theme-preview-buttons">ボタン</h2>
        <div className="theme-preview__row">
          <button type="button" className="btn btn-primary">
            出航する
          </button>
          <button type="button" className="btn">
            やめる
          </button>
          <button type="button" className="btn" disabled>
            出航できない
          </button>
        </div>
      </section>

      <section aria-labelledby="theme-preview-panels">
        <h2 id="theme-preview-panels">パネルと通知の吹き出し</h2>
        <div className="theme-preview__row">
          <div className="paper-panel theme-preview__panel">
            <h3>中通島</h3>
            <p>
              <span aria-hidden="true">★</span> 花火の予約 <span className="text-number">0:42</span>
            </p>
          </div>
          <div className="speech-bubble theme-preview__bubble">花火の予約、まだかなあ</div>
          <div className="theme-preview__storm">
            <p>嵐: 10 秒後に沖が荒れるぞ！</p>
          </div>
        </div>
      </section>

      <section aria-labelledby="theme-preview-space">
        <h2 id="theme-preview-space">余白と角丸</h2>
        <div className="theme-preview__row">
          {SPACES.map((name) => (
            <div key={name} className="theme-preview__space">
              <span style={{ width: `var(${name})`, height: `var(${name})` }} aria-hidden="true" />
              <code>{name}</code>
            </div>
          ))}
        </div>
        <div className="theme-preview__row">
          {RADII.map((name) => (
            <div
              key={name}
              className="theme-preview__radius"
              style={{ borderRadius: `var(${name})` }}
            >
              <code>{name}</code>
            </div>
          ))}
        </div>
      </section>
    </main>
  );
}
