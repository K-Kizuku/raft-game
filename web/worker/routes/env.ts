// Worker のバインディング（T-307）。値は web/cloudflare.config.ts（T-601）で入れる。

export interface Env {
  readonly ALLOWED_ORIGINS?: string; // 許可する Origin をカンマ区切りで（値は T-601）
  readonly DEMO_CONTROLS?: string; // "1" のときだけ demo: true を受け付ける
  readonly BUILD_ID?: string; // /api/healthz の build（値は T-601・T-603。なければ "dev"）
  readonly [binding: string]: unknown; // T-308〜T-310・T-601 が足すバインディング
}
