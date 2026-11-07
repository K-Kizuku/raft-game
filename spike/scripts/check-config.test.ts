import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { checkOutput, localImages, readOutput, type BuildOutput } from "./check-config.ts";

const fixtureDir = fileURLToPath(new URL("./fixtures/output", import.meta.url));

type Obj = Record<string, unknown>;

function fixture(): BuildOutput {
  return structuredClone(readOutput(fixtureDir));
}

function worker(out: BuildOutput): Obj {
  return out.worker as Obj;
}

function sub(o: Obj, key: string): Obj {
  return o[key] as Obj;
}

describe("check-config", () => {
  it("K-1: fixtures の正しい出力は成功する", () => {
    const out = fixture();
    expect(checkOutput(out)).toEqual([]);
    expect(localImages(out)).toEqual(["cloudflare-build/000000000000/raft-game-spike-server-server:fixture"]);
  });

  const cases: [string, (o: BuildOutput) => void, string][] = [
    ['name: "raft-game"', (o) => (worker(o)["name"] = "raft-game"), "name:"],
    ["domains がある", (o) => (worker(o)["domains"] = [{ hostname: "raft.kizuku-hackathon.work" }]), "domains:"],
    ["routes がある", (o) => (worker(o)["routes"] = ["raft.kizuku-hackathon.work/*"]), "routes:"],
    ["workersDev: false", (o) => (worker(o)["workersDev"] = false), "workersDev:"],
    ["previewUrls: true", (o) => (worker(o)["previewUrls"] = true), "previewUrls:"],
    ["limits がない", (o) => delete worker(o)["limits"], "limits.cpuMs:"],
    ["limits.cpuMs が整数でない", (o) => (sub(worker(o), "limits")["cpuMs"] = 50.5), "limits.cpuMs:"],
    [
      'DO のバインディングの worker: "raft-game"',
      (o) => (sub(sub(worker(o), "env"), "PLAIN")["worker"] = "raft-game"),
      "env.PLAIN.worker:",
    ],
    [
      "SpikeSession に container がない",
      (o) => delete sub(sub(worker(o), "exports"), "SpikeSession")["container"],
      "exports.SpikeSession.container:",
    ],
    [
      "PlainStore に container がある",
      (o) => (sub(sub(worker(o), "exports"), "PlainStore")["container"] = "raft-game-spike-server"),
      "exports.PlainStore.container:",
    ],
    [
      "ControlStore が SQLite でない",
      (o) => (sub(sub(worker(o), "exports"), "ControlStore")["storage"] = "legacy-kv"),
      "exports.ControlStore:",
    ],
    [
      "コンテナの名前が raft-game-server",
      (o) => ((o.containers[0]?.config as Obj)["name"] = "raft-game-server"),
      "containers.name:",
    ],
    [
      'schedulingPolicy: "default"',
      (o) => ((o.containers[0]?.config as Obj)["schedulingPolicy"] = "default"),
      "containers.schedulingPolicy:",
    ],
    [
      "SPIKE_TOKEN に値がある",
      (o) => (sub(sub(worker(o), "env"), "SPIKE_TOKEN")["value"] = "x".repeat(32)),
      "env.SPIKE_TOKEN:",
    ],
    [
      "SPIKE_TOKEN が text",
      (o) => (sub(worker(o), "env")["SPIKE_TOKEN"] = { type: "text", value: "x" }),
      "env.SPIKE_TOKEN:",
    ],
    ["コンテナの出力がない", (o) => (o.containers = []), "containers:"],
  ];

  it.each(cases)("K-1: %s は失敗し、理由に項目名が出る", (_name, mutate, expected) => {
    const out = fixture();
    mutate(out);
    const errors = checkOutput(out);
    expect(errors.length).toBeGreaterThan(0);
    expect(errors.some((e) => e.startsWith(expected))).toBe(true);
  });
});
