// 素の CSS ファイルの import（`import "./app.css"`）の型を Vite から読む（T-401）。
/// <reference types="vite/client" />

// ビルドのときの環境変数（T-413）。開発用のモック（T-402b）を止めるときは VITE_GAME_MOCK=off make dev。
interface ImportMetaEnv {
  readonly VITE_TURNSTILE_SITE_KEY?: string;
  readonly VITE_GAME_MOCK?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
