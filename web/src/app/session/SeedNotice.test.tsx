// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { SeedNotice } from "./SeedNotice.tsx";

afterEach(() => {
  cleanup();
});

describe("SeedNotice", () => {
  it("SN-1 ok はシードと再現されるものを出す", () => {
    render(<SeedNotice param={{ status: "ok", seed: 42 }} />);
    const status = screen.getByRole("status");
    expect(status.textContent).toBe(
      "シード 42 で遊びます。同じシードなら、依頼と嵐が同じ時刻に起きます。",
    );
    expect(status.className).toBe("session-notice seed-notice");
  });

  it("SN-1 invalid は正しくない旨を出す", () => {
    render(<SeedNotice param={{ status: "invalid" }} />);
    const status = screen.getByRole("status");
    expect(status.textContent).toBe(
      "シードの指定（?seed=）が正しくないため、新しいシードで遊びます。",
    );
    expect(status.className).toBe("session-notice seed-notice");
  });

  it("SN-1 none は何も描かない", () => {
    const { container } = render(<SeedNotice param={{ status: "none" }} />);
    expect(container.innerHTML).toBe("");
  });
});
