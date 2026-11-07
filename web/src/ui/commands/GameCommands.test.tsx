// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { GameCommandProvider, useGameCommand } from "./GameCommands.tsx";
import type { GameCommandSender } from "./GameCommands.tsx";

afterEach(() => {
  cleanup();
});

describe("GameCommands", () => {
  it("GC-1 Provider の外・send が null なら null、send を渡せばその関数", () => {
    let seen: GameCommandSender | null | undefined;
    function Probe(): ReactElement {
      seen = useGameCommand();
      return <p>probe</p>;
    }

    render(<Probe />);
    expect(seen).toBeNull();
    cleanup();

    seen = undefined;
    render(
      <GameCommandProvider send={null}>
        <Probe />
      </GameCommandProvider>,
    );
    expect(seen).toBeNull();
    cleanup();

    const fn: GameCommandSender = vi.fn(() => Promise.resolve({ cmdId: "c-1", ok: true, t: 0 }));
    seen = undefined;
    render(
      <GameCommandProvider send={fn}>
        <Probe />
      </GameCommandProvider>,
    );
    expect(seen).toBe(fn);
  });
});
