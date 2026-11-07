// Durable Object まわりの、使う分だけの構造の型（T-309・ADR-0023）。
// @cloudflare/workers-types と Workers のランタイムのモジュールを使わないため、ここで最小の形を決める。

export type SqlValue = string | number | null;

export interface SqlStorageLike {
  exec(query: string, ...bindings: SqlValue[]): { toArray(): Record<string, SqlValue>[] };
}

export interface DurableObjectStateLike {
  readonly storage: { readonly sql: SqlStorageLike; transactionSync<T>(fn: () => T): T };
}

export interface DurableObjectStubLike {
  fetch(request: Request): Promise<Response>;
}

export interface DurableObjectNamespaceLike {
  idFromName(name: string): unknown;
  get(id: unknown): DurableObjectStubLike;
}

function isNamespace(value: unknown): value is DurableObjectNamespaceLike {
  if (typeof value !== "object" || value === null) return false;
  if (!("idFromName" in value) || !("get" in value)) return false;
  return typeof value.idFromName === "function" && typeof value.get === "function";
}

// バインディング（env.COST_GUARD など）が Durable Object の名前空間の形なら、それを返す。
export function asDurableObjectNamespace(value: unknown): DurableObjectNamespaceLike | undefined {
  return isNamespace(value) ? value : undefined;
}
