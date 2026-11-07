// 遊び方（仮の文言）。Raft の用語は、設定でオンにしたときだけ併記する（ゲームデザイン §1・§8）。
// 見た目は T-424a: 便箋のカードに、番号つきの手順を絵（島・船・★）と並べる。
import type { ReactElement } from "react";

import { ArtIcon } from "../../ui/art/ArtIcon.tsx";
import { RaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { Link } from "../router/Link.tsx";
import "./HowToScreen.css";

export function HowToScreen(): ReactElement {
  return (
    <div className="app-screen app-card app-howto">
      <ArtIcon kind="letter" className="howto-stamp" />
      <h1 tabIndex={-1}>遊び方</h1>
      <ul className="howto-steps">
        <li className="howto-step">
          <ArtIcon kind="port" className="howto-step-icon" />
          島の港には、ほかの島あての
          <RaftTerm id="letter" />
          がたまります。
        </li>
        <li className="howto-step">
          <ArtIcon kind="ship" className="howto-step-icon" />
          船に手紙を積み、行き先を決めて出航しましょう。
        </li>
        <li className="howto-step">
          <ArtIcon kind="star" className="howto-step-icon" />
          お祭りの準備（花火や屋台の予約）を、島のみんなで
          <RaftTerm id="commit">確定</RaftTerm>
          させると得点になります。
        </li>
      </ul>
      <p className="howto-note">くわしい遊び方は準備中です。</p>
      <Link className="btn app-back" to={{ name: "title" }}>
        <ArtIcon kind="back" />
        タイトルへ
      </Link>
    </div>
  );
}
