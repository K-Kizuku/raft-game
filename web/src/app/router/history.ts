// History API の小さなルーター（T-401・DesignDoc §13.6）。依存を足さない。
import { useMemo, useSyncExternalStore } from "react";

import { formatRoute, parsePath, type KnownRoute, type Route } from "./routes.ts";

const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) {
    listener();
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    window.addEventListener("popstate", notify);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      window.removeEventListener("popstate", notify);
    }
  };
}

function getSnapshot(): string {
  return window.location.pathname;
}

export function navigate(route: KnownRoute, options?: { replace?: boolean }): void {
  const path = formatRoute(route);
  if (options?.replace) {
    window.history.replaceState(null, "", path);
  } else {
    window.history.pushState(null, "", path);
  }
  notify();
}

export function useRoute(): Route {
  const pathname = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  // 同じパスでは同じオブジェクトを返す（effect の依存に使えるように）。
  return useMemo(() => parsePath(pathname), [pathname]);
}
