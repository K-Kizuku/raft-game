// GET /api/stages（プロトコル §2）。
//
// 一覧は、ビルドのときに server/cmd/stagecatalog が data/ から作った stage-catalog.gen.json（T-311）。
// コンテナ・Durable Object・CostGuard に触れない（ADR-0018）。

import { autoFerryEnabled, type Env } from "./env.ts";
import { json } from "./http.ts";
import type { RequestContext, WorkerDeps } from "./ports.ts";

// 型の正本は Go の protocol.StageSummary（ADR-0014）。Worker の中に手書きで持ち、
// ずれは stage-catalog.ts の parseStageCatalog の厳密な検査で見つける。
export type Provenance = "fact" | "derived" | "creative";
export interface TextSource {
  readonly dataset: string;
  readonly region: string;
  readonly field: string;
  readonly quote: string | null;
}
export interface StageText {
  readonly text: string;
  readonly provenance: Provenance;
  readonly source: TextSource | null;
}
export interface StageSummary {
  readonly id: string;
  readonly title: string;
  readonly region: string;
  readonly islands: number;
  readonly ferries: number;
  readonly durationMs: number;
  readonly unlock: string | null;
  readonly intro: StageText;
  readonly learning: readonly string[];
  readonly demo: boolean;
}

// その Worker が見せるステージ。DEMO_CONTROLS が "1" のときだけデモ用のステージ（demo: true）を含める。
// "1" 以外（未設定・"0"・"true"・" 1" など）は一般公開と同じ扱い（sessions.ts の demo: true の判定と同じ）。
export function visibleStages(stages: readonly StageSummary[], env: Env): readonly StageSummary[] {
  if (env.DEMO_CONTROLS === "1") return stages;
  return stages.filter((stage) => !stage.demo);
}

// features は、その Worker で有効な任意の機能（Go の protocol.Features。T-724）。画面が切り替えを出すかを決める。
export interface Features {
  readonly autoFerry: boolean;
}

export function featuresOf(env: Env): Features {
  return { autoFerry: autoFerryEnabled(env) };
}

export function handleStages(ctx: RequestContext, deps: WorkerDeps): Response {
  return json({ stages: visibleStages(deps.stages, ctx.env), features: featuresOf(ctx.env) }, 200);
}
