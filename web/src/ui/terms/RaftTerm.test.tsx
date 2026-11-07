// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { RaftTerm, WithRaftTerm } from "./RaftTerm.tsx";
import { setShowRaftTerms, termsPreference } from "./preference.ts";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    termsPreference.reload();
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("RaftTerm", () => {
  it("TM-1 オフでは何も添えない", () => {
    const leader = render(<RaftTerm id="leader" />);
    expect(leader.container.textContent).toBe("代表");
    expect(leader.container.querySelector("small")).toBeNull();

    const letter = render(<WithRaftTerm raft="MsgAppResp">記録したよ！</WithRaftTerm>);
    expect(letter.container.textContent).toBe("記録したよ！");
    expect(letter.container.querySelector("small")).toBeNull();
  });

  it("TM-2 オンでは Raft の用語を添える", () => {
    act(() => {
      setShowRaftTerms(true);
    });

    const leader = render(<RaftTerm id="leader" />);
    expect(leader.container.textContent).toBe("代表（Leader）");
    const note = leader.container.querySelector("small.raft-term-note");
    expect(note?.getAttribute("data-raft-term")).toBe("Leader");
    expect(note?.querySelector('span[lang="en"]')?.textContent).toBe("Leader");

    const commit = render(<RaftTerm id="commit">確定</RaftTerm>);
    expect(commit.container.textContent).toBe("確定（Commit）");

    const request = render(<WithRaftTerm raft={null}>住民の依頼書</WithRaftTerm>);
    expect(request.container.textContent).toBe("住民の依頼書");
    expect(request.container.querySelector("small")).toBeNull();
  });

  it("TM-3 設定を変えると描き直される", () => {
    const { container } = render(<RaftTerm id="leader" />);
    expect(container.textContent).toBe("代表");

    act(() => {
      setShowRaftTerms(true);
    });
    expect(container.textContent).toBe("代表（Leader）");

    act(() => {
      setShowRaftTerms(false);
    });
    expect(container.textContent).toBe("代表");
  });
});
