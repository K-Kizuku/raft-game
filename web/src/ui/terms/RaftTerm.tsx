// Raft の用語の併記（T-412・FR-UI-10・DesignDoc §13.7）。設定がオフのときは何も足さない。
import type { ReactElement, ReactNode } from "react";

import { RAFT_TERMS, type RaftTermId } from "./glossary.ts";
import { useShowRaftTerms } from "./preference.ts";
import "./RaftTerm.css";

// 任意の言葉（手紙の名前など）。raft が null・"" なら何も添えない。
export function WithRaftTerm({
  raft,
  children,
}: {
  raft: string | null;
  children: ReactNode;
}): ReactElement {
  const show = useShowRaftTerms();
  if (!show || raft === null || raft === "") {
    return <>{children}</>;
  }
  return (
    <>
      {children}
      <small className="raft-term-note" data-raft-term={raft}>
        （<span lang="en">{raft}</span>）
      </small>
    </>
  );
}

// 用語集の言葉。children を省くと RAFT_TERMS[id].game を出す。活用形などは children で渡す。
export function RaftTerm({ id, children }: { id: RaftTermId; children?: ReactNode }): ReactElement {
  const entry = RAFT_TERMS[id];
  return <WithRaftTerm raft={entry.raft}>{children ?? entry.game}</WithRaftTerm>;
}
