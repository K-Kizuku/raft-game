// スパイク（T-007a / #134）の Worker の入口。本番の web/worker から import しない・されない。
import { handleRequest } from "./router.ts";
import type { Env } from "./types.ts";

export { ControlStore } from "./control.ts";
export { SpikeSession } from "./session.ts";
export { PlainStore } from "./stores.ts";

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    return handleRequest(request, env);
  },
};
