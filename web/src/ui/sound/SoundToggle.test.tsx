// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { SOUND_STORAGE_KEY, soundPreference } from "./preference.ts";
import { SoundToggle } from "./SoundToggle.tsx";

beforeEach(() => {
  localStorage.clear();
  act(() => {
    soundPreference.reload();
  });
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  act(() => {
    soundPreference.reload();
  });
});

describe("SoundToggle", () => {
  it("ST-1 既定はオン（押された状態）で、押すと消えてブラウザに保存する", () => {
    render(<SoundToggle />);
    const button = screen.getByRole("button", { name: "効果音" });
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(button.textContent).toBe("🔊効果音");

    fireEvent.click(button);
    expect(button.getAttribute("aria-pressed")).toBe("false");
    expect(button.textContent).toBe("🔇効果音");
    expect(localStorage.getItem(SOUND_STORAGE_KEY)).toBe('{"v":1,"enabled":false}');

    fireEvent.click(button);
    expect(button.getAttribute("aria-pressed")).toBe("true");
    expect(localStorage.getItem(SOUND_STORAGE_KEY)).toBe('{"v":1,"enabled":true}');
  });

  it("ST-2 保存した値で始まる", () => {
    localStorage.setItem(SOUND_STORAGE_KEY, '{"v":1,"enabled":false}');
    act(() => {
      soundPreference.reload();
    });
    render(<SoundToggle className="hud-sound" />);
    const button = screen.getByRole("button", { name: "効果音" });
    expect(button.getAttribute("aria-pressed")).toBe("false");
    expect(button.className).toBe("sound-toggle hud-sound");
  });
});
