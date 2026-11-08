// 絵の部品 1 つを、単独の小さな <svg> にしたアイコン（T-424a）。遊び方の図解・メニュー・戻るボタンで使う。
// 飾りなので aria-hidden にし、支援技術には読ませない（意味は隣の文字が伝える）。
import type { ReactElement } from "react";

import { CompassRose, Envelope, Island, MailShip, Star } from "./parts.tsx";

export type ArtIconKind = "island" | "port" | "ship" | "letter" | "star" | "compass" | "back";

const VIEW_BOXES: Readonly<Record<ArtIconKind, string>> = {
  island: "-86 -76 172 128",
  port: "-86 -76 172 128",
  ship: "-126 -94 196 124",
  letter: "-27 -20 54 40",
  star: "-23 -23 46 46",
  compass: "-66 -86 132 150",
  back: "0 0 24 24",
};

function Picture({ kind }: { kind: ArtIconKind }): ReactElement {
  switch (kind) {
    case "island":
      return <Island variant="house" />;
    case "port":
      // 港に手紙がたまっている島
      return (
        <g>
          <Island variant="house" />
          <Envelope x={52} y={-30} rotate={-10} scale={0.9} />
        </g>
      );
    case "ship":
      return <MailShip />;
    case "letter":
      return <Envelope />;
    case "star":
      return <Star />;
    case "compass":
      return <CompassRose />;
    case "back":
      return <path className="art-line" d="M15 5 L8 12 L15 19" strokeWidth={3} />;
  }
}

export function ArtIcon({
  kind,
  className,
}: {
  kind: ArtIconKind;
  className?: string;
}): ReactElement {
  return (
    <svg
      className={className === undefined ? "art art-icon" : `art art-icon ${className}`}
      viewBox={VIEW_BOXES[kind]}
      aria-hidden="true"
      focusable="false"
      data-art-icon={kind}
    >
      <Picture kind={kind} />
    </svg>
  );
}
