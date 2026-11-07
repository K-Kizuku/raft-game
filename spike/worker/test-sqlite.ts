// テスト用: node:sqlite の DatabaseSync(":memory:") を ctx.storage.sql（SqlStorageLike）の形に合わせる。
// tsconfig.worker.json は Node の型を読まないので、node:sqlite は動的に import し、使う所だけを構造の型で書く。
import type { SqlStorageLike } from "./types.ts";

interface StatementSyncLike {
  all(...params: (string | number | null)[]): unknown[];
}

interface DatabaseSyncLike {
  prepare(sql: string): StatementSyncLike;
  close(): void;
}

type DatabaseSyncCtor = new (path: string) => DatabaseSyncLike;

export interface TestSql {
  sql: SqlStorageLike;
  close(): void;
}

export async function openTestSql(): Promise<TestSql> {
  const specifier = "node:sqlite";
  const mod = (await import(/* @vite-ignore */ specifier)) as { DatabaseSync: DatabaseSyncCtor };
  const db = new mod.DatabaseSync(":memory:");
  const sql: SqlStorageLike = {
    exec(query, ...bindings) {
      // all() は SELECT 以外も実行し、行がなければ [] を返す
      const rows = db.prepare(query).all(...bindings) as Record<string, unknown>[];
      return { toArray: () => rows.map((r) => ({ ...r })) };
    },
  };
  return { sql, close: () => db.close() };
}
