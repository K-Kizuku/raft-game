// "cloudflare:workers" の最小の宣言（ControlStore が使う DurableObject の ctx・env だけ）。
// tsconfig.worker.json・tsconfig.node.json で使い、tsconfig.workerd.json（本物の型）では使わない。
declare module "cloudflare:workers" {
  export abstract class DurableObject<Env = unknown> {
    protected ctx: {
      readonly storage: {
        readonly sql: {
          exec(query: string, ...bindings: (string | number | null)[]): { toArray(): Record<string, unknown>[] };
        };
      };
    };
    protected env: Env;
    constructor(ctx: unknown, env: Env);
    fetch?(request: Request): Response | Promise<Response>;
  }
}
