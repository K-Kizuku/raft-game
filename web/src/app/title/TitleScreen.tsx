// タイトル（FR-UI-01・ゲームデザイン §7.1）。見た目は T-424a（温かい絵本・海図風）。
// タイトルの絵は人の素材（T-414）が届くまで、コードで描いた SVG（ui/art）で代える。
import type { ReactElement } from "react";

import { ArtIcon } from "../../ui/art/ArtIcon.tsx";
import { TitleArt } from "../../ui/art/TitleArt.tsx";
import { TITLE } from "../constants.ts";
import { Link } from "../router/Link.tsx";
import "./TitleScreen.css";

export function TitleScreen(): ReactElement {
  return (
    <div className="app-screen app-title">
      <TitleArt className="title-backdrop" />
      <h1 className="title-heading" tabIndex={-1}>
        {TITLE}
      </h1>
      <p className="title-tagline">嵐の島々に、手紙で合意を届けよう</p>
      <nav className="title-nav" aria-label="メニュー">
        <ul className="app-menu title-menu">
          <li className="title-menu-item title-menu-item-play">
            <Link className="btn btn-primary title-menu-card" to={{ name: "stageSelect" }}>
              <ArtIcon kind="ship" className="title-menu-icon" />
              遊ぶ
            </Link>
          </li>
          <li className="title-menu-item">
            <Link className="btn title-menu-card" to={{ name: "howto" }}>
              <ArtIcon kind="letter" className="title-menu-icon" />
              遊び方
            </Link>
          </li>
          <li className="title-menu-item">
            <Link className="btn title-menu-card" to={{ name: "credits" }}>
              <ArtIcon kind="star" className="title-menu-icon" />
              クレジット
            </Link>
          </li>
          <li className="title-menu-item">
            <Link className="btn title-menu-card" to={{ name: "settings" }}>
              <ArtIcon kind="compass" className="title-menu-icon" />
              設定
            </Link>
          </li>
        </ul>
      </nav>
    </div>
  );
}
