// パネルからコマンドを送るための Context（T-406）。スロットは props を取らないので、
// WS の接続（GameConnection.sendCommand）を作る側（T-402b）が GameCommandProvider で送る関数を渡す。
// この部品はコマンドを溜めず、作り直さない。結果（ack）の扱いは使う側が決める。
import { createContext, useContext } from "react";
import type { ReactElement, ReactNode } from "react";

import type { Ack, ClientMessage } from "../../protocol/index.ts";

export type GameCommandType = Exclude<
  ClientMessage["type"],
  "hello" | "ping" | "cmd.sync" | "sub.engineer"
>;
export type GameCommandPayload<T extends GameCommandType> = Extract<
  ClientMessage,
  { type: T }
>["payload"];
export type GameCommandSender = <T extends GameCommandType>(
  type: T,
  payload: GameCommandPayload<T>,
) => Promise<Ack>;

const GameCommandContext = createContext<GameCommandSender | null>(null);

export function GameCommandProvider({
  send,
  children,
}: {
  send: GameCommandSender | null;
  children: ReactNode;
}): ReactElement {
  return <GameCommandContext.Provider value={send}>{children}</GameCommandContext.Provider>;
}

// Provider の外・send が null なら null。使う側は null のときコマンドの操作を無効にする。
export function useGameCommand(): GameCommandSender | null {
  return useContext(GameCommandContext);
}
