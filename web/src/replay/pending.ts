// 次に再生する入力ログを渡す口（ADR-0028 R4・T-720d）。R5（リザルトの「このラウンドをリプレイ」）が使う。
// モジュールの中に 1 件だけ持ち、取り出したら消す。入力ログの中身を console に出さない。

let queued: string | null = null;

// queueReplayInput は次に /replay を開いたときに再生する入力ログ（ファイルの中身そのまま）を入れる。
// 前に入れたものがあれば置き換える。
export function queueReplayInput(text: string): void {
  queued = text;
}

// takeQueuedReplayInput は入れた入力ログを取り出して消す。無ければ null。
export function takeQueuedReplayInput(): string | null {
  const text = queued;
  queued = null;
  return text;
}
