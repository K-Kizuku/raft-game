// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { navigate, useRoute } from "./history.ts";
import { Link } from "./Link.tsx";
import { formatRoute, type KnownRoute } from "./routes.ts";

function ShowRoute() {
  const route = useRoute();
  return (
    <p data-testid="route">
      {route.name === "stageIntro" ? `stageIntro:${route.stageId}` : route.name}
    </p>
  );
}

function shownRoute(): string | null {
  return screen.getByTestId("route").textContent;
}

// jsdom が本当に遷移しようとしないよう、React のリスナーより後で既定の動作を止める。
let defaultPrevented: boolean[] = [];
function recordAndPrevent(event: MouseEvent) {
  defaultPrevented.push(event.defaultPrevented);
  event.preventDefault();
}

beforeEach(() => {
  window.history.replaceState(null, "", "/");
  defaultPrevented = [];
  window.addEventListener("click", recordAndPrevent);
});

afterEach(() => {
  window.removeEventListener("click", recordAndPrevent);
  cleanup();
});

describe("navigate", () => {
  // NV-1
  it("push で pathname が変わり履歴が 1 増え、replace では増えない", () => {
    render(<ShowRoute />);
    expect(shownRoute()).toBe("title");
    const before = window.history.length;

    act(() => {
      navigate({ name: "stageSelect" });
    });
    expect(window.location.pathname).toBe("/stages");
    expect(window.history.length).toBe(before + 1);
    expect(shownRoute()).toBe("stageSelect");

    act(() => {
      navigate({ name: "stageIntro", stageId: "stage1" }, { replace: true });
    });
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(window.history.length).toBe(before + 1);
    expect(shownRoute()).toBe("stageIntro:stage1");
  });
});

describe("popstate", () => {
  // NV-2
  it("popstate で useRoute が新しいルートを返す", () => {
    render(<ShowRoute />);
    act(() => {
      window.history.replaceState(null, "", "/credits");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(shownRoute()).toBe("credits");
  });
});

describe("Link", () => {
  // NV-3
  const to: KnownRoute = { name: "stageIntro", stageId: "stage1" };

  it("href が formatRoute(to) になる", () => {
    render(<Link to={to}>行く</Link>);
    expect(screen.getByRole("link", { name: "行く" }).getAttribute("href")).toBe(formatRoute(to));
  });

  it("左クリックで遷移し、既定の動作を止める", () => {
    render(
      <>
        <Link to={to}>行く</Link>
        <ShowRoute />
      </>,
    );
    const before = window.history.length;
    const notCanceled = fireEvent.click(screen.getByRole("link", { name: "行く" }));
    expect(notCanceled).toBe(false);
    expect(defaultPrevented).toEqual([true]);
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(window.history.length).toBe(before + 1);
    expect(shownRoute()).toBe("stageIntro:stage1");
  });

  it("replace なら履歴を増やさない", () => {
    render(
      <Link to={to} replace>
        行く
      </Link>,
    );
    const before = window.history.length;
    fireEvent.click(screen.getByRole("link", { name: "行く" }));
    expect(window.location.pathname).toBe("/stages/stage1");
    expect(window.history.length).toBe(before);
  });

  const modified: ReadonlyArray<[string, MouseEventInit]> = [
    ["Ctrl", { ctrlKey: true }],
    ["Meta", { metaKey: true }],
    ["Shift", { shiftKey: true }],
    ["Alt", { altKey: true }],
    ["中クリック", { button: 1 }],
  ];
  it.each(modified)("%s では止めず、遷移しない", (_label, init) => {
    render(<Link to={to}>行く</Link>);
    fireEvent.click(screen.getByRole("link", { name: "行く" }), init);
    expect(defaultPrevented).toEqual([false]);
    expect(window.location.pathname).toBe("/");
  });
});
