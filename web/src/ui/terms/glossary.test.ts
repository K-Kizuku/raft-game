import { describe, expect, it } from "vitest";

import { LETTER_RAFT_TERMS, letterRaftTerm, RAFT_TERMS, withRaftTerm } from "./glossary.ts";

describe("RAFT_TERMS", () => {
  it("GL-1 表のとおり", () => {
    expect(RAFT_TERMS).toEqual({
      leader: { game: "代表", raft: "Leader" },
      follower: { game: "一般の島", raft: "Follower" },
      preCandidate: { game: "立候補の相談中", raft: "PreCandidate" },
      candidate: { game: "立候補中", raft: "Candidate" },
      term: { game: "代表の任期", raft: "Term" },
      ledger: { game: "共和国の台帳", raft: "Log" },
      entry: { game: "記録", raft: "Log Entry" },
      commit: { game: "共和国で確定", raft: "Commit" },
      apply: { game: "確定", raft: "Apply" },
      quorum: { game: "過半数", raft: "Quorum" },
      node: { game: "島", raft: "Node" },
      letter: { game: "手紙", raft: "Message" },
      heartbeat: { game: "定期便", raft: "Heartbeat" },
      vote: { game: "投票", raft: "Vote" },
      electionTimeout: { game: "定期便が途切れる", raft: "Election Timeout" },
      checkQuorum: { game: "孤立した代表が退く", raft: "CheckQuorum" },
      partition: { game: "嵐", raft: "Network Partition" },
      staleLetter: { game: "古い手紙", raft: "古い Term" },
    });
    for (const entry of Object.values(RAFT_TERMS)) {
      expect(entry.raft).not.toMatch(/[（）]/);
    }
  });
});

describe("letterRaftTerm", () => {
  it.each<[string, string | null]>([
    ["heartbeat", "MsgHeartbeat"],
    ["heartbeat_resp", "MsgHeartbeatResp"],
    ["append", "MsgApp"],
    ["append_probe", "MsgApp"],
    ["append_resp", "MsgAppResp"],
    ["append_reject", "MsgAppResp・Reject"],
    ["prevote", "MsgPreVote"],
    ["prevote_resp", "MsgPreVoteResp"],
    ["vote", "MsgVote"],
    ["vote_resp", "MsgVoteResp"],
    ["request", null],
    ["other", null],
    ["foo", null],
    ["", null],
    ["toString", null],
    ["__proto__", null],
  ])("GL-2 %j → %j", (label, raft) => {
    expect(letterRaftTerm(label)).toBe(raft);
  });

  it("GL-2 表の label は 12 個", () => {
    expect(Object.keys(LETTER_RAFT_TERMS)).toHaveLength(12);
  });
});

describe("withRaftTerm", () => {
  it.each<[string, string | null, boolean, string]>([
    ["代表", "Leader", true, "代表（Leader）"],
    ["代表", "Leader", false, "代表"],
    ["お元気ですか？", null, true, "お元気ですか？"],
    ["代表", "", true, "代表"],
  ])("GL-3 (%j, %j, %j) →「%s」", (game, raft, show, text) => {
    expect(withRaftTerm(game, raft, show)).toBe(text);
  });
});
