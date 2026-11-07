// タイトル（FR-UI-01・ゲームデザイン §7.1）。タイトルの絵は T-414。
import type { ReactElement } from "react";

import { TITLE } from "../constants.ts";
import { Link } from "../router/Link.tsx";

export function TitleScreen(): ReactElement {
  return (
    <div className="app-screen app-title">
      <h1 tabIndex={-1}>{TITLE}</h1>
      <nav aria-label="メニュー">
        <ul className="app-menu">
          <li>
            <Link to={{ name: "stageSelect" }}>遊ぶ</Link>
          </li>
          <li>
            <Link to={{ name: "howto" }}>遊び方</Link>
          </li>
          <li>
            <Link to={{ name: "credits" }}>クレジット</Link>
          </li>
          <li>
            <Link to={{ name: "settings" }}>設定</Link>
          </li>
        </ul>
      </nav>
    </div>
  );
}
