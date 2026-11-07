// 遊び方（仮の文言）。Raft の用語を使わない（ゲームデザイン §1・§8）。
import type { ReactElement } from "react";

import { Link } from "../router/Link.tsx";

export function HowToScreen(): ReactElement {
  return (
    <div className="app-screen app-howto">
      <h1 tabIndex={-1}>遊び方</h1>
      <ul>
        <li>島の港には、ほかの島あての手紙がたまります。</li>
        <li>船に手紙を積み、行き先を決めて出航しましょう。</li>
        <li>お祭りの準備（花火や屋台の予約）を、島のみんなで確定させると得点になります。</li>
      </ul>
      <p>くわしい遊び方は準備中です。</p>
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}
