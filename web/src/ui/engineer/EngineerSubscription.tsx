// エンジニアモードの購読（sub.engineer）の関数を受け取る Context（T-407・DesignDoc §13.4）。
// WS の接続は画面への接続のタスク（T-402b）が作り、
// subscribe={(p) => connection.setEngineerSubscription(p)} のように渡す。ここでは web/src/net/ を import しない。
// sub.engineer は ack を待たない電文なので、GameCommandSender（web/src/ui/commands/）とは別の Context にする。
import { createContext, useContext } from "react";
import type { ReactElement, ReactNode } from "react";

import type { SubEngineerPayload } from "../../protocol/index.ts";

export type EngineerSubscriber = (payload: SubEngineerPayload) => void;

const EngineerSubscriptionContext = createContext<EngineerSubscriber | null>(null);

export function EngineerSubscriptionProvider(props: {
  subscribe: EngineerSubscriber | null;
  children: ReactNode;
}): ReactElement {
  return (
    <EngineerSubscriptionContext.Provider value={props.subscribe}>
      {props.children}
    </EngineerSubscriptionContext.Provider>
  );
}

// Provider の外では null。
export function useEngineerSubscriber(): EngineerSubscriber | null {
  return useContext(EngineerSubscriptionContext);
}
