// GET /api/healthz（プロトコル §2）。

import type { Env } from "./env.ts";
import { json } from "./http.ts";

export function handleHealthz(env: Env): Response {
  const build = typeof env.BUILD_ID === "string" && env.BUILD_ID !== "" ? env.BUILD_ID : "dev";
  return json({ status: "ok", build }, 200);
}
