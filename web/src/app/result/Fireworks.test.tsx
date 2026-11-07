// @vitest-environment jsdom
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { FIREWORK_BURSTS, Fireworks } from "./Fireworks.tsx";

afterEach(() => {
  cleanup();
});

function burstPositions(container: HTMLElement): [string, string][] {
  return Array.from(container.querySelectorAll<HTMLElement>(".firework")).map((el) => [
    el.style.left,
    el.style.top,
  ]);
}

describe("Fireworks", () => {
  it("FW-1 文字の帯と、決まった位置の花火", () => {
    const { container } = render(<Fireworks />);

    const region = screen.getByRole("region", { name: "花火大会" });
    expect(within(region).getByText("花火大会")).toBeDefined();

    const sky = container.querySelector(".fireworks-sky");
    expect(sky?.getAttribute("aria-hidden")).toBe("true");
    const bursts = sky?.querySelectorAll<HTMLElement>(".firework") ?? [];
    expect(bursts).toHaveLength(FIREWORK_BURSTS.length);
    expect(FIREWORK_BURSTS).toHaveLength(8);
    FIREWORK_BURSTS.forEach((burst, i) => {
      const el = bursts[i];
      expect(el?.dataset["burst"]).toBe(String(i));
      expect(el?.style.left).toBe(`${burst.x}%`);
      expect(el?.style.top).toBe(`${burst.y}%`);
      expect(el?.style.animationDelay).toBe(`${burst.delayMs}ms`);
    });

    const first = burstPositions(container);
    cleanup();
    const again = render(<Fireworks />);
    expect(burstPositions(again.container)).toEqual(first);

    for (const tag of ["img", "audio", "canvas"]) {
      expect(again.container.querySelector(tag)).toBeNull();
    }
  });

  it("FW-2 位置と遅れの範囲", () => {
    for (const burst of FIREWORK_BURSTS) {
      expect(Number.isInteger(burst.x)).toBe(true);
      expect(Number.isInteger(burst.y)).toBe(true);
      expect(Number.isInteger(burst.delayMs)).toBe(true);
      expect(burst.x).toBeGreaterThanOrEqual(0);
      expect(burst.x).toBeLessThanOrEqual(100);
      expect(burst.y).toBeGreaterThanOrEqual(0);
      expect(burst.y).toBeLessThanOrEqual(100);
      expect(burst.delayMs).toBeGreaterThanOrEqual(0);
      expect(burst.delayMs).toBeLessThanOrEqual(1400);
    }
  });
});
