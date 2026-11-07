// クレジット（FR-UI-07・データ仕様 §6）。素材ごとの出典（CREDITS.md）は T-414 が足す。
import type { ReactElement, ReactNode } from "react";

import { Link } from "../router/Link.tsx";

const REPOSITORY_URL = "https://github.com/K-Kizuku/raft-game";

// JSX の改行で空白が入らないよう、文字列で持つ（データ仕様 §6 の 1〜2 行目）。
const OPEN_DATA_SOURCE =
  "本ゲームは、国土交通省「Project LINKS アイディアソン公開データ（2026年度）離島に関するデータ」" +
  "（離島振興対策実施地域一覧・離島振興計画書）を加工して作成しています。";

function ExternalLink({ href, children }: { href: string; children: ReactNode }): ReactElement {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  );
}

export function CreditsScreen(): ReactElement {
  return (
    <div className="app-screen app-credits">
      <h1 tabIndex={-1}>クレジット</h1>
      <section aria-label="オープンデータ">
        <p>{OPEN_DATA_SOURCE}</p>
        <p>
          出典：国土交通省 Project LINKS（
          <ExternalLink href="https://www.mlit.go.jp/links/">
            https://www.mlit.go.jp/links/
          </ExternalLink>
          ）
        </p>
        <p>利用規約：政府標準利用規約（第2.0版）</p>
        <p>
          航行時間・積載量・イベントなどの多くはゲーム上の設定であり、実際の航路・運航とは異なります。
        </p>
        <p>
          ゲーム内の船の名前の一部は、離島振興計画書に記載された船名を使用しており、実在の運航会社とは関係ありません。
        </p>
      </section>
      <section aria-label="ソフトウェア">
        <p>etcd-io/raft — Copyright The etcd Authors — Apache License 2.0</p>
        <p>
          （乱数源を注入する変更を加えています。詳細は server/third_party/etcd-raft/PATCHES.md）
        </p>
        <p>
          このゲームのソースコードは Apache License 2.0 で公開しています。（
          <ExternalLink href={`${REPOSITORY_URL}/blob/main/LICENSE`}>LICENSE</ExternalLink>・
          <ExternalLink href={`${REPOSITORY_URL}/blob/main/NOTICE`}>NOTICE</ExternalLink>）
        </p>
      </section>
      <section aria-label="素材">
        <p>画像・効果音の一部は画像生成 AI 等で作成しています。</p>
      </section>
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}
