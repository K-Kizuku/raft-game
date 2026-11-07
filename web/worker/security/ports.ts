// 既定の配線（T-307）。T-310 が sessionId の署名・Turnstile・レート制限の部品に置き換える。
// 部品がない（undefined）間、ルーターは POST /api/sessions を 503、/ws/sessions/{id} を 404 にする。

import type { Env } from "../routes/env.ts";
import type { SecurityPorts } from "../routes/ports.ts";

export const createSecurityPorts: (env: Env) => SecurityPorts | undefined = () => undefined;
