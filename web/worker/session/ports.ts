// 既定の配線（T-307）。T-308 が GameSession の作成と接続の部品に置き換える。
// 部品がない（undefined）間、ルーターは POST /api/sessions を 503、/ws/sessions/{id} を 404 にする。

import type { Env } from "../routes/env.ts";
import type { SessionPorts } from "../routes/ports.ts";

export const createSessionPorts: (env: Env) => SessionPorts | undefined = () => undefined;
