// テスト用: node:sqlite の DatabaseSync(":memory:") を DurableObjectStateLike の形に合わせる。
// 本番のコードからは import しない。
//
// `import "node:sqlite"` と静的に書くと types: [] の typecheck が通らないため、動的に読む。

import type { DurableObjectStateLike, SqlStorageLike, SqlValue } from "./cf-types.ts";

interface StatementSyncLike {
  all(...bindings: SqlValue[]): Record<string, unknown>[];
}

interface DatabaseSyncLike {
  prepare(query: string): StatementSyncLike;
  exec(query: string): void;
}

type DatabaseSyncConstructor = new (path: string) => DatabaseSyncLike;

let cached: DatabaseSyncConstructor | undefined;

async function loadDatabaseSync(): Promise<DatabaseSyncConstructor> {
  if (cached === undefined) {
    const spec: string = "node:sqlite";
    const mod = (await import(/* @vite-ignore */ spec)) as {
      DatabaseSync: DatabaseSyncConstructor;
    };
    cached = mod.DatabaseSync;
  }
  return cached;
}

function toSqlValue(value: unknown): SqlValue {
  if (value === null || typeof value === "string" || typeof value === "number") return value;
  throw new Error(`test-sqlite: unsupported SQL value ${typeof value}`);
}

export interface TestStorage {
  readonly sql: SqlStorageLike;
  transactionSync<T>(fn: () => T): T;
}

export interface TestState extends DurableObjectStateLike {
  readonly storage: TestStorage;
}

// テストごとに新しい ":memory:" の DB を作る。
export async function createTestState(): Promise<TestState> {
  const DatabaseSync = await loadDatabaseSync();
  const db = new DatabaseSync(":memory:");
  const sql: SqlStorageLike = {
    exec(query, ...bindings) {
      const rows = db.prepare(query).all(...bindings);
      return {
        toArray: () =>
          rows.map((row) => {
            const plain: Record<string, SqlValue> = {};
            for (const [key, value] of Object.entries({ ...row })) plain[key] = toSqlValue(value);
            return plain;
          }),
      };
    },
  };
  const storage: TestStorage = {
    sql,
    transactionSync<T>(fn: () => T): T {
      db.exec("BEGIN");
      try {
        const result = fn();
        db.exec("COMMIT");
        return result;
      } catch (error) {
        db.exec("ROLLBACK");
        throw error;
      }
    },
  };
  return { storage };
}
