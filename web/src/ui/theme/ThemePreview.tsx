// デザインの見本の画面（T-420）。開発のときだけ、URL の `?theme-preview` で開く（App.tsx）。
// 本番のビルドには入らない（App.tsx が import.meta.env.DEV のときだけ遅延読み込みする）。
import type { CSSProperties, ReactElement, ReactNode } from "react";

import type { Letter } from "../../protocol/index.ts";
import { LetterIcon } from "../letters/LetterIcon.tsx";
import { ProvenanceLabel } from "../provenance/ProvenanceLabel.tsx";
import { tokens, type ColorTokenName } from "./tokens.ts";
import "./ThemePreview.css";
import "../engineer/EngineerPanel.css";
import "../demo/DemoKeys.css";
import "../demo/DemoStormFlavor.css";
import "../../app/tutorial/TutorialOverlay.css";
import "../opening/OpeningCeremony.css";

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
  const solo = soloFrameId();
  if (solo !== null) {
    return (
      <main>
        <OverlayPreview solo={solo} />
      </main>
    );
  }
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

      <OverlayPreview />

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

// ゲーム中に重ねる部品の見本（T-425a）。部品と同じクラス名・CSS で、ストアを使わずに静的に描く。
// 枠に contain: layout を付けると、中の position: fixed の部品は枠を基準に置かれる（実際の画面と同じ位置関係で見える）。
// 島の色はステージ定義のものを使う決まりなので、見本ではトークンの色で代える。
const PREVIEW_ISLANDS = [
  { id: 1, name: "中通島", color: "var(--color-postal)", role: "leader", label: "Leader" },
  { id: 2, name: "福江島", color: "var(--color-sea-deep)", role: "follower", label: "Follower" },
  {
    id: 3,
    name: "小値賀島",
    color: "var(--color-land-green)",
    role: "candidate",
    label: "Candidate",
  },
  { id: 4, name: "宇久島", color: "var(--color-gold)", role: "follower", label: "Follower" },
] as const;

function previewLetter(id: number, type: string, label: Letter["label"], stale = false): Letter {
  return {
    id,
    kind: "raft",
    from: 1,
    to: 2,
    createdAt: 12_000,
    loc: { type: "port", islandId: 1 },
    label,
    raft: {
      type,
      term: 3,
      logTerm: 3,
      index: 7,
      commit: 6,
      entries: [],
      reject: false,
      rejectHint: 0,
      stale,
    },
  };
}

const PREVIEW_LETTERS: readonly Letter[] = [
  previewLetter(41, "MsgApp", "append"),
  previewLetter(42, "MsgVote", "vote"),
  previewLetter(43, "MsgHeartbeat", "heartbeat", true),
];

const PREVIEW_FIELDS: readonly (readonly [string, string])[] = [
  ["Type", "MsgVote"],
  ["From", "1"],
  ["To", "2"],
  ["Term", "3"],
  ["LogTerm", "3"],
  ["Index", "7"],
];

const FRAME_STYLE: CSSProperties = {
  position: "relative",
  width: "100vw",
  height: "56.25rem",
  marginLeft: "calc(50% - 50vw)",
  marginTop: "var(--space-4)",
  overflow: "hidden",
  contain: "layout paint",
  background:
    "radial-gradient(circle at 30% 40%, var(--color-sea-light), var(--color-sea-mid) 60%, var(--color-sea-deep))",
};

// 1 つの枠だけを画面いっぱいに出すとき（スクリーンショット用）の style。
const SOLO_FRAME_STYLE: CSSProperties = {
  ...FRAME_STYLE,
  position: "fixed",
  inset: 0,
  width: "100vw",
  height: "100vh",
  margin: 0,
};

const FRAME_IDS = ["preview-engineer", "preview-storm", "preview-tutorial", "preview-opening"];

// URL が #preview-engineer などなら、その枠だけを画面いっぱいに出す（1600×900 のスクリーンショット用）。
function soloFrameId(): string | null {
  const id = window.location.hash.slice(1);
  return FRAME_IDS.includes(id) ? id : null;
}

function OverlayFrame({
  id,
  title,
  solo,
  children,
}: {
  id: string;
  title: string;
  solo: string | null;
  children: ReactNode;
}): ReactElement | null {
  if (solo !== null) {
    if (solo !== id) return null;
    return (
      <div id={id} style={SOLO_FRAME_STYLE}>
        {children}
      </div>
    );
  }
  return (
    <div className="theme-preview__overlay">
      <h3>{title}</h3>
      <div id={id} style={FRAME_STYLE}>
        {children}
      </div>
    </div>
  );
}

function OverlayPreview({ solo = null }: { solo?: string | null }): ReactElement {
  return (
    <section aria-labelledby="theme-preview-overlays">
      <h2 id="theme-preview-overlays" hidden={solo !== null}>
        ゲーム中に重ねる部品
      </h2>
      <p className="text-muted" hidden={solo !== null}>
        エンジニアパネル・デモ操作のキー・嵐の紹介・チュートリアル・開会式の見本（静的な見本で、操作はできません）。
      </p>

      <OverlayFrame solo={solo} id="preview-engineer" title="エンジニアパネルとデモ操作のキー">
        <PreviewEngineerPanel />
        <aside aria-label="デモ操作の見本" className="demo-keys">
          <ul className="demo-keys-list">
            <li>
              <kbd>D</kbd> 嵐を起こす
            </li>
            <li>
              <kbd>F</kbd> 押している間 4 倍速（<kbd>Shift</kbd> で 8 倍速）
            </li>
            <li>
              <kbd>N</kbd> 次の出来事まで進めて止まる
            </li>
            <li>
              <kbd>Space</kbd> 一時停止・再開
            </li>
          </ul>
          <p className="demo-keys-fast">早送り中 ×4</p>
          <p className="demo-keys-status">いまは嵐を起こせません。</p>
        </aside>
      </OverlayFrame>

      <OverlayFrame solo={solo} id="preview-storm" title="デモの嵐の紹介">
        <section aria-label="嵐の知らせの見本" className="demo-storm-flavor">
          <p className="demo-storm-flavor-text">台風が近づき、福江島のまわりの海が荒れています</p>
          <p className="demo-storm-flavor-quote">「台風等の自然災害の影響を受けやすい」</p>
          <ProvenanceLabel provenance="derived" />
        </section>
      </OverlayFrame>

      <OverlayFrame solo={solo} id="preview-tutorial" title="チュートリアルの吹き出し">
        <button
          type="button"
          className="btn"
          style={{ position: "absolute", left: "40rem", top: "20rem" }}
        >
          小豆島
        </button>
        <div
          className="tutorial-highlight"
          aria-hidden="true"
          style={{ left: "39.5rem", top: "19.5rem", width: "7.5rem", height: "3.75rem" }}
        />
        <aside className="tutorial" aria-label="チュートリアルの見本" style={{ top: "2rem" }}>
          <span className="tutorial__speaker" aria-hidden="true">
            局
          </span>
          <p className="tutorial__text">
            小豆島の港に手紙が出ました。船『せとうち丸』に積んでみましょう（ドラッグ）
          </p>
          <div className="tutorial__actions">
            <button type="button" className="btn btn-primary">
              わかった
            </button>
            <button type="button" className="btn">
              チュートリアルを終える
            </button>
          </div>
        </aside>
        <aside
          className="tutorial tutorial--collapsed"
          aria-label="チュートリアルの見本（たたんだ形）"
          style={{ top: "34rem" }}
        >
          <span className="tutorial__speaker" aria-hidden="true">
            局
          </span>
          <p className="tutorial__text">行き先に豊島を選んで、出航！</p>
          <div className="tutorial__actions">
            <button type="button" className="btn btn-primary">
              開く
            </button>
            <button type="button" className="btn">
              チュートリアルを終える
            </button>
          </div>
        </aside>
      </OverlayFrame>

      <OverlayFrame solo={solo} id="preview-opening" title="開会式">
        <div className="opening-ceremony__confetti" aria-hidden="true">
          {Array.from({ length: 16 }, (_, i) => (
            <span key={i} />
          ))}
        </div>
        <section className="opening-ceremony" aria-label="開会式の見本">
          <h2 className="opening-ceremony__title">開会式</h2>
          <p className="opening-ceremony__message">
            代表（Leader）に
            <span className="opening-ceremony__island">
              <span
                className="opening-ceremony__dot"
                style={{ backgroundColor: "var(--color-postal)" }}
                aria-hidden="true"
              />
              <span aria-hidden="true">👑</span>
              中通島
            </span>
            が選ばれました
          </p>
          <button type="button" className="btn opening-ceremony__close">
            閉じる
          </button>
          <details className="opening-ceremony__election">
            <summary>選挙の記録</summary>
            <p>Term 2</p>
          </details>
        </section>
      </OverlayFrame>
    </section>
  );
}

function PreviewEngineerPanel(): ReactElement {
  return (
    <>
      <button type="button" className="engineer-toggle" aria-expanded="true">
        E エンジニアモード
      </button>
      <aside aria-label="エンジニアパネルの見本" className="engineer-panel">
        <div className="engineer-header">
          <h2>エンジニアモード</h2>
          <button type="button">閉じる</button>
        </div>
        <div className="engineer-body">
          <section className="engineer-section">
            <h3>ノード一覧</h3>
            <table aria-label="ノード一覧の見本" className="engineer-table">
              <thead>
                <tr>
                  {["Node", "Role", "Term", "Vote", "Lead", "Commit", "Applied", "Last Index"].map(
                    (name) => (
                      <th key={name} scope="col">
                        {name}
                      </th>
                    ),
                  )}
                </tr>
              </thead>
              <tbody>
                {PREVIEW_ISLANDS.map((island) => {
                  const candidate = island.role === "candidate";
                  return (
                    <tr key={island.id} data-role={island.role === "leader" ? "leader" : undefined}>
                      <td>
                        <button
                          type="button"
                          className="engineer-node"
                          aria-pressed={island.id === 1}
                        >
                          <span
                            className="engineer-node-dot"
                            aria-hidden="true"
                            style={{ backgroundColor: island.color }}
                          />
                          {island.id} {island.name}
                        </button>
                      </td>
                      <td className={`engineer-role engineer-role--${island.role}`}>
                        {island.label}
                      </td>
                      <td>{candidate ? 4 : 3}</td>
                      <td>{candidate ? 3 : 1}</td>
                      <td>{candidate ? "—" : 1}</td>
                      <td>6</td>
                      <td>6</td>
                      <td>7</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </section>
          <section className="engineer-section">
            <h3>手紙</h3>
            <div className="engineer-letters">
              <h4>港: 中通島</h4>
              <ul>
                {PREVIEW_LETTERS.map((letter) => (
                  <li key={letter.id}>
                    <button type="button" aria-pressed={letter.id === 42}>
                      <LetterIcon letter={letter} borderColor="var(--color-sea-deep)" />
                      <span>
                        #{letter.id} {letter.raft?.type} 1→2
                      </span>
                      {letter.raft?.stale === true && <span className="engineer-stale">stale</span>}
                    </button>
                  </li>
                ))}
                <li className="engineer-more">+3 通</li>
              </ul>
            </div>
          </section>
          <section className="engineer-section engineer-letter-detail">
            <h3>手紙の詳細 #42</h3>
            <p className="engineer-loc">港: 中通島</p>
            <dl className="engineer-fields">
              {PREVIEW_FIELDS.map(([name, value]) => (
                <div key={name}>
                  <dt>{name}</dt>
                  <dd className={name === "Type" ? "engineer-msg-type" : undefined}>{value}</dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      </aside>
    </>
  );
}
