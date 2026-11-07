// 既定の配線（T-307）。T-309 が CostGuard の予約・取り消し・管理用 API の部品に置き換える。
// 部品がない（undefined）間、ルーターは POST /api/sessions を 503、/api/admin/guards を 404 にする。

import type { Env } from "../routes/env.ts";
import type { CostGuardPorts } from "../routes/ports.ts";

export const createCostGuardPorts: (env: Env) => CostGuardPorts | undefined = () => undefined;
