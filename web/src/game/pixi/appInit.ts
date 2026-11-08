// マップの PixiJS の Application を作る（T-HF2・#386）。ブラウザ・GPU によって app.init が失敗することがあるので、
// 設定を変えて 2 回まで試し直す。キャンバスの大きさ × devicePixelRatio が WebGL の上限
// （MAX_TEXTURE_SIZE・MAX_RENDERBUFFER_SIZE）を超えないように、resolution を抑える。
import { Application } from "pixi.js";
import type { ApplicationOptions } from "pixi.js";

import type { Size } from "../model/viewport.ts";

export type GlLimits = Readonly<{ maxTextureSize: number; maxRenderbufferSize: number }>;

// 1 回の試しの設定（Pixi の既定の順は webgl → webgpu → canvas。2 回目は webgpu を先にする）。
export type InitAttempt = Readonly<{
  preference: "webgl" | "webgpu";
  // 抑える前の resolution の上限（devicePixelRatio か 1）。
  resolution: number;
  antialias: boolean;
}>;

function validRatio(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 1;
}

// 1 回目は今までどおり（webgl・devicePixelRatio・antialias）。2 回目は webgpu を先に、
// 3 回目は resolution: 1・antialias: false で試す。
export function initAttempts(dpr: number): readonly InitAttempt[] {
  const ratio = validRatio(dpr);
  return [
    { preference: "webgl", resolution: ratio, antialias: true },
    { preference: "webgpu", resolution: ratio, antialias: true },
    { preference: "webgl", resolution: 1, antialias: false },
  ];
}

// キャンバスの長い辺 × resolution が上限を超えないように resolution を抑える（上限が分からなければ want のまま）。
export function clampResolution(size: Size, want: number, limits: GlLimits | null): number {
  const ratio = validRatio(want);
  if (limits === null) return ratio;
  const max = Math.min(limits.maxTextureSize, limits.maxRenderbufferSize);
  if (!Number.isFinite(max) || max <= 0) return ratio;
  const longest = Math.max(size.width, size.height);
  if (!Number.isFinite(longest) || longest <= 0 || longest * ratio <= max) return ratio;
  // 画素の数は丸めて決まるので、少し小さめ（0.01 刻みで切り捨て）にする。
  const fit = Math.floor((max / longest) * 100) / 100;
  return fit > 0 ? fit : max / longest;
}

// WebGL の上限を読む。読めなければ null（そのときは resolution を抑えない）。
export function readGlLimits(): GlLimits | null {
  if (typeof document === "undefined") return null;
  try {
    const canvas = document.createElement("canvas");
    const gl = (canvas.getContext("webgl2") ?? canvas.getContext("webgl")) as
      WebGLRenderingContext | WebGL2RenderingContext | null;
    if (gl === null) return null;
    const limits = {
      maxTextureSize: Number(gl.getParameter(gl.MAX_TEXTURE_SIZE)),
      maxRenderbufferSize: Number(gl.getParameter(gl.MAX_RENDERBUFFER_SIZE)),
    };
    // 調べるためだけの WebGL のコンテキストはすぐ手放す（ブラウザが持てる数には上限がある）。
    gl.getExtension("WEBGL_lose_context")?.loseContext();
    const ok = (n: number): boolean => Number.isFinite(n) && n > 0;
    return ok(limits.maxTextureSize) && ok(limits.maxRenderbufferSize) ? limits : null;
  } catch {
    return null;
  }
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export type CreatedApp = Readonly<{ app: Application; attempt: InitAttempt }>;

// 試しの設定を順に試し、最初に成功した Application を返す。すべて失敗したら、それぞれの理由を並べて reject する。
export async function createApplication(
  host: HTMLElement,
  options: Readonly<{
    dpr: number;
    limits: GlLimits | null;
    base: Partial<ApplicationOptions>;
  }>,
): Promise<CreatedApp> {
  const size = { width: host.clientWidth, height: host.clientHeight };
  const failures: string[] = [];
  const errors: unknown[] = [];
  for (const attempt of initAttempts(options.dpr)) {
    const app = new Application();
    try {
      await app.init({
        ...options.base,
        resizeTo: host,
        preference: attempt.preference,
        antialias: attempt.antialias,
        autoDensity: true,
        resolution: clampResolution(size, attempt.resolution, options.limits),
      });
      return { app, attempt };
    } catch (error) {
      errors.push(error);
      failures.push(
        `${attempt.preference}・resolution ${attempt.resolution}・antialias ${attempt.antialias}: ${errorMessage(error)}`,
      );
      console.warn("[map] app.init failed", attempt, error);
      try {
        app.destroy({ removeView: true }, { children: true });
      } catch {
        // 作りかけの Application は壊せないことがある（renderer が無い）。元の失敗を返す。
      }
    }
  }
  throw new Error(`app.init が ${failures.length} 回とも失敗: ${failures.join(" / ")}`, {
    cause: errors.length === 1 ? errors[0] : errors,
  });
}
