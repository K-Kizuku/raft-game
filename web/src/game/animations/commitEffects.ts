// 依頼の確定の演出を決める純粋な関数（T-435 / DesignDoc §13.3・docs/11-visual-design.md）。
// 依頼が確定したとき（request.updated で status が succeeded になったとき）、代表の島から依頼した島へ
// 光る線を伸ばし、依頼した島に紙吹雪を散らす。サーバーが出した状態を見せるだけで、確定や代表を
// ここで決めない（代表は node.state の値を roleMarkOf で読むだけ）。ストアに書かない。
// 引数を書き換えず、時刻は引数（now = 実時間の performance.now）だけを使う。Math.random を使わない。
//
// 画面の作り方の判断:
// - マップを開いたときにすでに確定している依頼は演出しない（createCommitTracker の最初の take は []）。
//   見たことのない依頼が、はじめから確定していたとき（再接続の snapshot など）も演出しない。
// - 進みは実時間（now）で、ゲームの速度・一時停止に関係なく同じ速さで動く。
// - 同時に描くのは maxEffects まで。超えたら startedAt の古いものから捨てる（同時に複数が確定しても重くしない）。
// - 動きを減らす設定のときは、光る線だけ（伸ばさずに出して消す）。紙吹雪は出さない。
import type { Request } from "../../protocol/index.ts";
import type { World } from "../../state/index.ts";
import { createRandom, mixSeed } from "../model/art/random.ts";
import { roleMarkOf } from "../model/scene.ts";
import type { MapScene } from "../model/scene.ts";
import { MAP_STYLE } from "../model/style.ts";
import type { Point } from "../model/viewport.ts";
import { COMMIT_EFFECT_STYLE } from "./style.ts";

const C = COMMIT_EFFECT_STYLE;

export type CommitEffect = Readonly<{
  key: string; // 表示物の鍵（依頼の ID と始まった時刻）
  requestId: string;
  from: number | null; // 代表の島（代表がいない・依頼した島が代表なら null で、線を引かない）
  to: number; // 依頼した島（request.origin）
  color: string; // 依頼した島の色（stage に無ければ MAP_STYLE.colors.unknownIsland）
  seed: number; // 紙吹雪の散り方の種（依頼の ID から作る）
  startedAt: number; // 実時間（performance.now）
  durationMs: number;
}>;

export type CommitTracker = Readonly<{ take(world: World): Request[] }>;

// createCommitTracker は、前に見たときから確定（succeeded）に変わった依頼を、出てきた順に返す。
export function createCommitTracker(): CommitTracker {
  const statuses = new Map<string, Request["status"]>();
  let seenRequests: World["requests"] | null = null;
  let first = true;
  return {
    take(world) {
      if (world.requests === seenRequests) return [];
      seenRequests = world.requests;
      const fresh: Request[] = [];
      for (const id of world.requestIds) {
        const request = world.requests[id];
        if (request === undefined) continue;
        const before = statuses.get(id);
        statuses.set(id, request.status);
        if (first || before === undefined) continue;
        if (before !== "succeeded" && request.status === "succeeded") fresh.push(request);
      }
      first = false;
      return fresh;
    },
  };
}

// leaderIslandOf は、いまの代表の島（roleMarkOf が "leader" の島のうち、ステージの並びで最初のもの）。
// いなければ null。届いた値を見せるだけで、どれが正しい代表かを決めない。
export function leaderIslandOf(world: World): number | null {
  for (const id of world.stage.islandIds) {
    if (roleMarkOf(world.nodes, id) === "leader") return id;
  }
  return null;
}

// 文字列の依頼の ID から紙吹雪の種を作る（FNV-1a）。
function seedOf(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i += 1) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

function isFinished(effect: CommitEffect, now: number): boolean {
  return now >= effect.startedAt + effect.durationMs;
}

export function planCommitEffects(
  active: readonly CommitEffect[],
  fresh: readonly Request[],
  ctx: Readonly<{ world: World; now: number; reducedMotion: boolean }>,
): CommitEffect[] {
  const { world, now, reducedMotion } = ctx;
  let effects = active.filter((e) => !isFinished(e, now));
  if (fresh.length === 0) return effects;
  const leader = leaderIslandOf(world);
  for (const request of fresh) {
    const to = request.origin;
    const from = leader === null || leader === to ? null : leader;
    // 動きを減らす設定では紙吹雪を出さないので、線が無ければ何も描かない。
    if (reducedMotion && from === null) continue;
    // 同じ依頼の演出は重ねず、新しいもので置き換える（リプレイで巻き戻して進め直したときなど）。
    effects = effects.filter((e) => e.requestId !== request.id);
    effects.push({
      key: `${request.id}@${String(now)}`,
      requestId: request.id,
      from,
      to,
      color: world.stage.islands[to]?.color ?? MAP_STYLE.colors.unknownIsland,
      seed: seedOf(request.id),
      startedAt: now,
      durationMs: reducedMotion ? C.reducedMs : C.durationMs,
    });
  }
  if (effects.length > C.maxEffects) {
    // 安定な並べ替えなので、startedAt が同じなら後から来たもの（新しい確定）が残る。
    effects = [...effects].sort((a, b) => a.startedAt - b.startedAt).slice(-C.maxEffects);
  }
  return effects;
}

export type CommitLine = Readonly<{
  start: Point; // 代表の島の側の端
  end: Point; // 依頼した島の側の端
  head: Point; // 伸びている線の先（伸びきったら end）
  alpha: number;
}>;

export type ConfettiPiece = Readonly<{
  at: Point;
  rotation: number;
  alpha: number;
  color: number;
}>;

export type CommitFrame = Readonly<{
  line: CommitLine | null;
  confetti: readonly ConfettiPiece[];
}>;

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v));
}

function centerOf(scene: MapScene, id: number): Point | null {
  return scene.islands.find((i) => i.id === id)?.at ?? null;
}

// commitLine は、代表の島から依頼した島への線の両端（島の絵に重ねないよう、中心から inset だけ離す）。
// 島が近すぎて両端が重なるなら null。
export function commitLine(
  from: Point,
  to: Point,
  inset: number,
): { start: Point; end: Point } | null {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const dist = Math.hypot(dx, dy);
  if (dist <= inset * 2) return null;
  const ux = dx / dist;
  const uy = dy / dist;
  return {
    start: { x: from.x + ux * inset, y: from.y + uy * inset },
    end: { x: to.x - ux * inset, y: to.y - uy * inset },
  };
}

// confettiPieces は、依頼した島の中心 center の周りの紙吹雪。q は紙吹雪の進み（0〜1）、k は絵の拡大率。
export function confettiPieces(center: Point, seed: number, q: number, k: number): ConfettiPiece[] {
  if (q <= 0 || q >= 1) return [];
  const ease = 1 - (1 - q) * (1 - q);
  const alpha = q < 0.6 ? 1 : (1 - q) / 0.4;
  const pieces: ConfettiPiece[] = [];
  for (let i = 0; i < C.confettiCount; i += 1) {
    const rnd = createRandom(mixSeed(seed, i));
    const angle = rnd() * 2 * Math.PI;
    const dist = C.confettiSpread * (0.5 + 0.5 * rnd()) * k;
    const spin = (rnd() * 2 - 1) * C.confettiSpin;
    pieces.push({
      at: {
        x: center.x + Math.cos(angle) * dist * ease,
        y:
          center.y +
          Math.sin(angle) * dist * 0.6 * ease -
          C.confettiRise * k * ease +
          C.confettiFall * k * q * q,
      },
      rotation: spin * q,
      alpha,
      color: C.colors.confetti[i % C.colors.confetti.length] ?? C.colors.line,
    });
  }
  return pieces;
}

// commitEffectFrame は、いまの時刻 now での演出の位置。位置は毎フレームの scene から求めるので、
// 画面の大きさが変わってもずれない。終わった・依頼した島が scene に無いなら null。
export function commitEffectFrame(
  effect: CommitEffect,
  scene: MapScene,
  now: number,
  reducedMotion: boolean,
): CommitFrame | null {
  if (isFinished(effect, now)) return null;
  const target = centerOf(scene, effect.to);
  if (target === null) return null;
  const k = scene.markScale ?? 1;
  const p = clamp01(effect.durationMs > 0 ? (now - effect.startedAt) / effect.durationMs : 1);

  let line: CommitLine | null = null;
  const source = effect.from === null ? null : centerOf(scene, effect.from);
  const ends = source === null ? null : commitLine(source, target, C.lineInset * k);
  if (ends !== null) {
    const fade = p < C.lineFadeRatio ? 1 : (1 - p) / (1 - C.lineFadeRatio);
    if (reducedMotion) {
      line = { ...ends, head: ends.end, alpha: fade };
    } else {
      const g = clamp01(p / C.lineGrowRatio);
      const grow = 1 - (1 - g) * (1 - g);
      line = {
        ...ends,
        head: {
          x: ends.start.x + (ends.end.x - ends.start.x) * grow,
          y: ends.start.y + (ends.end.y - ends.start.y) * grow,
        },
        alpha: fade,
      };
    }
  }

  let confetti: ConfettiPiece[] = [];
  if (!reducedMotion) {
    // 線があれば、線が依頼した島に届いてから散らす。
    const begin = line === null ? 0 : C.lineGrowRatio;
    const q = (p - begin) / (1 - begin);
    confetti = confettiPieces(target, effect.seed, q, k);
  }
  return { line, confetti };
}
