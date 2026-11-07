// GET /api/stages（プロトコル §2）。

import { json } from "./http.ts";
import type { WorkerDeps } from "./ports.ts";

export interface StageSummary {
  readonly id: string;
  readonly title: string;
  readonly region: string;
  readonly islands: number;
  readonly ferries: number;
  readonly durationMs: number;
  readonly unlock: string | null;
}

export function handleStages(deps: WorkerDeps): Response {
  return json({ stages: deps.stages }, 200);
}
