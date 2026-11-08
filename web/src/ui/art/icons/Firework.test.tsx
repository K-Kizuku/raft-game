// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { FireworkIcon } from "./Firework.tsx";

afterEach(() => {
  cleanup();
});

describe("FireworkIcon", () => {
  it("FI-1 aria-hidden で、文字を持たず、文字の大きさ（1em）で並ぶ", () => {
    const { container } = render(<FireworkIcon />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("focusable")).toBe("false");
    expect(svg?.getAttribute("width")).toBe("1em");
    expect(svg?.getAttribute("height")).toBe("1em");
    expect(svg?.getAttribute("fill")).toBe("currentColor");
    expect(svg?.getAttribute("stroke")).toBe("currentColor");
    expect(svg?.style.color).toBe("");
    expect(svg?.textContent).toBe("");
    expect(svg?.querySelectorAll("line").length).toBeGreaterThan(0);
    expect(svg?.querySelectorAll("circle").length).toBeGreaterThan(0);
  });

  it("FI-2 色のトークンとクラスを受け取る", () => {
    const { container } = render(<FireworkIcon tone="--color-gold" className="x" />);
    const svg = container.querySelector("svg");
    expect(svg?.style.color).toBe("var(--color-gold)");
    expect(svg?.classList.contains("firework-icon")).toBe(true);
    expect(svg?.classList.contains("x")).toBe(true);
  });
});
