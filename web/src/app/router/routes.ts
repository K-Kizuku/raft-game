// 画面のルート（T-401・DesignDoc §13.6）。純粋な関数だけを置く。
// 後続のタスクがルートを足すときは、このファイルと App の分岐を変える。

export type Route =
  | { name: "title" } // /
  | { name: "howto" } // /howto
  | { name: "credits" } // /credits
  | { name: "settings" } // /settings
  | { name: "stageSelect" } // /stages
  | { name: "stageIntro"; stageId: string } // /stages/<stageId>
  | { name: "game" } // /play
  | { name: "result" } // /result
  | { name: "replay" } // /replay（ADR-0028 R4）
  | { name: "notFound" };

export type KnownRoute = Exclude<Route, { name: "notFound" }>;

export const STAGE_ID_PATTERN: RegExp = /^[a-z0-9][a-z0-9-]{0,31}$/;

const STATIC_PATHS: Readonly<Record<string, KnownRoute>> = {
  "/": { name: "title" },
  "/howto": { name: "howto" },
  "/credits": { name: "credits" },
  "/settings": { name: "settings" },
  "/stages": { name: "stageSelect" },
  "/play": { name: "game" },
  "/result": { name: "result" },
  "/replay": { name: "replay" },
};

// `location.pathname` だけを受ける（クエリと `#` は呼ぶ側で落とす）。
// 末尾の `/` は 1 つだけ許す。大文字・小文字は区別する。パーセントエンコードは解かない。
export function parsePath(pathname: string): Route {
  if (pathname === "/") {
    return { name: "title" };
  }
  const path = pathname.endsWith("/") ? pathname.slice(0, -1) : pathname;
  // `//` や `/stages//` のように、末尾の `/` が 2 つ以上あるものは合わない。
  if (path === "" || path.endsWith("/")) {
    return { name: "notFound" };
  }
  if (Object.hasOwn(STATIC_PATHS, path)) {
    const route = STATIC_PATHS[path];
    if (route) {
      return { ...route };
    }
  }
  const prefix = "/stages/";
  if (path.startsWith(prefix)) {
    const stageId = path.slice(prefix.length);
    if (STAGE_ID_PATTERN.test(stageId)) {
      return { name: "stageIntro", stageId };
    }
  }
  return { name: "notFound" };
}

export function formatRoute(route: KnownRoute): string {
  switch (route.name) {
    case "title":
      return "/";
    case "howto":
      return "/howto";
    case "credits":
      return "/credits";
    case "settings":
      return "/settings";
    case "stageSelect":
      return "/stages";
    case "stageIntro":
      if (!STAGE_ID_PATTERN.test(route.stageId)) {
        throw new Error(`不正なステージ ID: ${JSON.stringify(route.stageId)}`);
      }
      return `/stages/${route.stageId}`;
    case "game":
      return "/play";
    case "result":
      return "/result";
    case "replay":
      return "/replay";
  }
}
