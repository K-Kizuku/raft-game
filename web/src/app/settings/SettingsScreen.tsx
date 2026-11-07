// 設定（仮）。中身と保存は T-412 が書き換える。
import type { ReactElement } from "react";

import { Link } from "../router/Link.tsx";

export function SettingsScreen(): ReactElement {
  return (
    <div className="app-screen app-settings">
      <h1 tabIndex={-1}>設定</h1>
      <p>設定は準備中です。</p>
      <Link to={{ name: "title" }}>タイトルへ</Link>
    </div>
  );
}
