// 素の CSS ファイルの import（`import "./app.css"`）の型を Vite から読む（T-401）。
/// <reference types="vite/client" />

// ビルドのときの環境変数（T-413）。
interface ImportMetaEnv {
  readonly VITE_TURNSTILE_SITE_KEY?: string;
}
