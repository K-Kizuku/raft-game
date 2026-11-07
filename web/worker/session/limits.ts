// GameSession の上限の定数（T-308・DesignDoc §12.3・§12.5）。
//
// T-007（#15）の測定の結果で変わる値（コンテナの起動を待つ上限・マシンの大きさ）はここに書かず、
// Env の設定にする（config.ts・admin/ports.ts の containerInstanceOf）。

export const CONTAINER_PORT = 8080; // env: { MODE: "single", PORT: "8080" }
export const IMAGE_NAME = "server"; // ctx.container.images["server"]（T-601 がこの名前で定義する）

export const IDLE_MS = 5 * 60 * 1000; // 切断されてからの保持時間（security/session-id.ts の GRACE_MINUTES）
export const DEFAULT_MAX_MINUTES = 30; // view() が読めないとき
export const DEFAULT_MAX_ALARMS = 20; // view() が読めないとき

export const READY_POLL_INTERVAL_MS = 100; // /healthz を試す間隔
export const READY_POLL_REQUEST_MS = 2000; // /healthz の 1 回の要求の打ち切り（残り時間と小さいほう）

export const MAX_WS_MESSAGE_BYTES = 16384; // ブラウザの電文の上限（UTF-8 のバイト数）
export const MAX_MESSAGES_PER_SECOND = 30; // 1 秒の枠ごとのブラウザの電文の上限
export const MAX_BUFFERED_MESSAGES = 32; // コンテナにつながるまでに溜めるブラウザの電文の数
export const MAX_RESULT_BYTES = 262144; // 保存する internal.result の payload の上限

// destroy() が失敗したとき、後始末をやり直すアラームの間隔（回数は max_alarms の中に数える）。
export const DESTROY_RETRY_MS = 30 * 1000;

// T-308b が使う（入力ログの記録と復元）。
export const MAX_CONTAINER_STARTS = 3; // 最初の 1 回＋復元 2 回
export const MAX_INPUT_LOG_ROWS = 10000;
export const MAX_INPUT_LOG_BYTES = 524288; // cmd の JSON の合計
export const CLOCK_PERSIST_INTERVAL_MS = 5000; // last_clock_t を SQLite に書く間隔の下限
