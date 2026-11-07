// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { App } from "./App.tsx";

afterEach(() => {
  cleanup();
});

describe("App", () => {
  it("タイトル「合意、乗船中。」を描く", () => {
    render(<App />);

    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("合意、乗船中。");
  });
});
