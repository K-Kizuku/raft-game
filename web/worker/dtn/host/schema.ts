// ActorHost の保存の形（DTN-12・docs/dtn/persistence-and-recovery.md §2）。Go の SQLite adapter（DTN-07）と同じ SQL。
// sql.exec には 1 回に 1 つの文だけを渡す。

import type { SqlStorageLike } from "../../admin/cf-types.ts";

export const HOST_SCHEMA_VERSION = "1";

export const HOST_SCHEMA: readonly string[] = [
  "CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)",
  `CREATE TABLE IF NOT EXISTS journal (
    event_seq   INTEGER PRIMARY KEY,
    kind        TEXT    NOT NULL,
    req_digest  TEXT    NOT NULL,
    payload     TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    response    TEXT,
    resp_digest TEXT,
    epoch       INTEGER NOT NULL
  )`,
  `CREATE TABLE IF NOT EXISTS bundles (
    letter_id   INTEGER PRIMARY KEY,
    bundle      BLOB,
    digest      TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    state       TEXT    NOT NULL,
    place       TEXT    NOT NULL,
    transfer_id TEXT,
    updated_seq INTEGER NOT NULL
  )`,
  `CREATE TABLE IF NOT EXISTS transfers (
    transfer_id TEXT PRIMARY KEY,
    role        TEXT NOT NULL,
    peer        TEXT NOT NULL,
    contact_id  TEXT NOT NULL,
    letter_ids  TEXT NOT NULL,
    digests     TEXT NOT NULL,
    state       TEXT NOT NULL,
    ack         TEXT
  )`,
  "CREATE TABLE IF NOT EXISTS contacts (contact_id TEXT PRIMARY KEY, body TEXT NOT NULL, state TEXT NOT NULL)",
];

export function ensureHostSchema(sql: SqlStorageLike): void {
  for (const statement of HOST_SCHEMA) sql.exec(statement);
}
