// Worker のバインディング（T-307）。値は web/cloudflare.config.ts（T-601）で入れる。

export interface Env {
  readonly ALLOWED_ORIGINS?: string; // 許可する Origin をカンマ区切りで（値は T-601）
  readonly DEMO_CONTROLS?: string; // "1" のときだけ demo: true を受け付ける
  readonly BUILD_ID?: string; // /api/healthz の build（値は T-601・T-603。なければ "dev"）
  readonly AUTO_FERRY?: string; // "1" のときだけ自動航行（cmd.ferry.auto）を受け付ける（T-724。既定は "1"）
  readonly [binding: string]: unknown; // T-308〜T-310・T-601 が足すバインディング
}

// 自動航行（cmd.ferry.auto。T-724）が有効か。AUTO_FERRY が "1" のときだけ（"1" 以外・未設定は無効）。
// GET /api/stages の features.autoFerry と、GameSession が internal.init・internal.restore に載せる autoFerry に使う。
export function autoFerryEnabled(env: Env): boolean {
  return env.AUTO_FERRY === "1";
}
