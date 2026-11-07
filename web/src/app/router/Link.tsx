import type { MouseEvent, ReactElement, ReactNode } from "react";

import { navigate } from "./history.ts";
import { formatRoute, type KnownRoute } from "./routes.ts";

export type LinkProps = {
  to: KnownRoute;
  replace?: boolean;
  children: ReactNode;
  className?: string;
};

// `<a href>` を出し、修飾キーの無い左クリックだけを SPA の遷移にする。
// 中クリック・Ctrl+クリックなどはブラウザに任せる（新しいタブなど）。
export function Link({ to, replace, children, className }: LinkProps): ReactElement {
  const href = formatRoute(to);
  const onClick = (event: MouseEvent<HTMLAnchorElement>) => {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    event.preventDefault();
    navigate(to, { replace: replace ?? false });
  };
  return (
    <a href={href} className={className} onClick={onClick}>
      {children}
    </a>
  );
}
