// Contact（寄港）の形と署名（DTN-12・docs/dtn/handoff-and-contact.md §2）。
//
// sig = base64url(HMAC-SHA256(DTN_CONTACT_KEY, sig を除いた RFC 8785 の正準の JSON))
// 署名するのは Session Front（DtnSession）だけ。確かめるのは Actor Host。

import { canonicalJson, createHmac, type Hmac } from "./codec.ts";
import {
  CONTACT_ID_PATTERN,
  isObject,
  isSafeNonNegative,
  ProtocolError,
  SESSION_ID_PATTERN,
  type JsonObject,
} from "./protocol.ts";

export interface Contact {
  readonly contactId: string;
  readonly sessionId: string;
  readonly island: number;
  readonly ferry: string;
  readonly islandEpoch: number;
  readonly ferryEpoch: number;
  readonly openedAt: number;
  readonly sig?: string;
}

const FERRY_ID = /^[A-Za-z0-9_-]{1,32}$/;
const CONTACT_KEYS: ReadonlySet<string> = new Set([
  "contactId",
  "sessionId",
  "island",
  "ferry",
  "islandEpoch",
  "ferryEpoch",
  "openedAt",
  "sig",
]);

// DTN_CONTACT_KEY の規則（SESSION_SIGNING_KEY と同じ: 表示できる ASCII で 32〜256 文字）。
const CONTACT_KEY_PATTERN = /^[\x21-\x7E]{32,256}$/;

export function contactKeyOf(env: Readonly<Record<string, unknown>>): string | undefined {
  const key = env["DTN_CONTACT_KEY"];
  return typeof key === "string" && CONTACT_KEY_PATTERN.test(key) ? key : undefined;
}

export function parseContact(value: unknown): Contact {
  if (!isObject(value)) throw new ProtocolError("invalid", "contact");
  if (Object.keys(value).some((key) => !CONTACT_KEYS.has(key))) {
    throw new ProtocolError("invalid", "contact keys");
  }
  const { contactId, sessionId, island, ferry, islandEpoch, ferryEpoch, openedAt, sig } = value;
  if (
    typeof contactId !== "string" ||
    !CONTACT_ID_PATTERN.test(contactId) ||
    typeof sessionId !== "string" ||
    !SESSION_ID_PATTERN.test(sessionId) ||
    !isSafeNonNegative(island) ||
    island < 1 ||
    typeof ferry !== "string" ||
    !FERRY_ID.test(ferry) ||
    !isSafeNonNegative(islandEpoch) ||
    !isSafeNonNegative(ferryEpoch) ||
    !isSafeNonNegative(openedAt) ||
    (sig !== undefined && typeof sig !== "string")
  ) {
    throw new ProtocolError("invalid", "contact fields");
  }
  return {
    contactId,
    sessionId,
    island,
    ferry,
    islandEpoch,
    ferryEpoch,
    openedAt,
    ...(sig === undefined ? {} : { sig }),
  };
}

function unsignedText(contact: Contact): string {
  const { sig: _sig, ...body } = contact;
  void _sig;
  return canonicalJson(body);
}

export interface ContactSigner {
  sign(contact: Contact): Promise<Contact>;
  verify(contact: Contact): Promise<boolean>;
}

export function createContactSigner(key: string): ContactSigner {
  const hmac: Hmac = createHmac(key);
  return {
    async sign(contact) {
      return { ...contact, sig: await hmac.sign(unsignedText(contact)) };
    },
    async verify(contact) {
      if (typeof contact.sig !== "string") return false;
      return hmac.verify(unsignedText(contact), contact.sig);
    },
  };
}

export function contactJson(contact: Contact): JsonObject {
  return { ...contact };
}

// Actor の名前が Contact の当事者か。当事者なら相手の名前を返す。
export function counterpartOf(contact: Contact, actor: string): string | null {
  if (actor === `island:${contact.island}`) return `ferry:${contact.ferry}`;
  if (actor === `ferry:${contact.ferry}`) return `island:${contact.island}`;
  return null;
}

// Contact に書かれた、その Actor の epoch。
export function contactEpochOf(contact: Contact, actor: string): number | null {
  if (actor === `island:${contact.island}`) return contact.islandEpoch;
  if (actor === `ferry:${contact.ferry}`) return contact.ferryEpoch;
  return null;
}
