// 遊び方（T-417）。目的・画面の見方・操作・代表と確定・嵐・キー操作の 6 つの節を、便箋のカードに並べる。
// 文言はゲームデザイン（docs/04）と実際の画面に合わせた AI の下書きで、オーナーが直す（#132 の D-11）。
// Raft の用語は、設定でオンにしたときだけ併記する（ゲームデザイン §8）。見た目は T-424a。
import type { ReactElement } from "react";

import { ArtIcon } from "../../ui/art/ArtIcon.tsx";
import { RaftTerm } from "../../ui/terms/RaftTerm.tsx";
import { Link } from "../router/Link.tsx";
import "./HowToScreen.css";

export function HowToScreen(): ReactElement {
  return (
    <div className="app-screen app-card app-howto">
      <ArtIcon kind="letter" className="howto-stamp" />
      <h1 tabIndex={-1}>遊び方</h1>
      <ol className="howto-steps">
        <li className="howto-step">
          <ArtIcon kind="star" className="howto-step-icon" />
          <h2 className="howto-step-title">目的</h2>
          <p>
            島々は、力を合わせてお祭りの準備（花火や屋台の予約）を進めています。あなたは郵便船の船長です。島から島へ
            <RaftTerm id="letter" />
            を運び、島の依頼を
            <RaftTerm id="apply" />
            させましょう。
          </p>
          <div className="howto-list">
            <p>★1: 確定した依頼の数が、ステージの目標に届く</p>
            <p>★2: ★1 に加えて、花火の予約がすべて確定する（花火大会！）</p>
            <p>★3: ★2 に加えて、スコアが高得点の目標に届く</p>
          </div>
        </li>
        <li className="howto-step">
          <ArtIcon kind="port" className="howto-step-icon" />
          <h2 className="howto-step-title">画面の見方</h2>
          <div className="howto-list">
            <p>
              上:
              残り時間・スコア・確定した依頼の数・フェス準備度（花火は確定すると点灯）と、一時停止・速度のボタン
            </p>
            <p>中央: マップ。島・航路・船と、港にたまった手紙の数が見えます</p>
            <p>右: マップで選んだ島の詳細（役割・満足度・港の手紙・依頼の状態）</p>
            <p>下: フェリーの操作パネル（積み込み・行き先・出航）</p>
            <p>マップの右上: 島の郵便局長からのお知らせ</p>
          </div>
        </li>
        <li className="howto-step">
          <ArtIcon kind="ship" className="howto-step-icon" />
          <h2 className="howto-step-title">操作</h2>
          <div className="howto-list">
            <p>マップか下のパネルで、船を選びます</p>
            <p>港の手紙を押す（ドラッグでも）と船に積みます。積み荷を押すと港に戻せます</p>
            <p>
              行き先を選んで「出航」。着いた島で、その島あての手紙が配達されます。返事はその船で持ち帰れます
            </p>
            <p>「おまかせ」を押すと、行き先あての手紙を大事なものから積みます</p>
          </div>
        </li>
        <li className="howto-step">
          <ArtIcon kind="island" className="howto-step-icon" />
          <h2 className="howto-step-title">代表と確定</h2>
          <p>
            島の中に 1 つ、
            <RaftTerm id="leader" />
            の島（👑）があります。依頼書は、まず代表の島へ運びます。代表は、どの島にもある
            <RaftTerm id="ledger" />
            に書く内容を、手紙でほかの島へ送ります。
            <RaftTerm id="quorum" />
            の島が記録して返事が代表に届くと「
            <RaftTerm id="commit" />
            」。その結果が依頼元の島に届くと、依頼が確定して得点になります。
          </p>
        </li>
        <li className="howto-step">
          <ArtIcon kind="compass" className="howto-step-icon" />
          <h2 className="howto-step-title">嵐</h2>
          <p>
            <RaftTerm id="partition" />
            が来た航路（点線と雲）からは出航できません。郵便局長が 10
            秒前に予報します。島が分かれると、過半数の島がつながっている側でしか確定は進みません。代表からの
            <RaftTerm id="heartbeat" />
            が途切れた島は代表に
            <RaftTerm id="candidate">立候補</RaftTerm>
            し、過半数の
            <RaftTerm id="vote">票</RaftTerm>
            を集めると新しい代表になります。みんなの返事が届かない代表は、自分から代表をおります。
          </p>
        </li>
        <li className="howto-step">
          <ArtIcon kind="letter" className="howto-step-icon" />
          <h2 className="howto-step-title">キー操作</h2>
          <dl className="howto-keys">
            <dt>
              <kbd>Space</kbd>
            </dt>
            <dd>一時停止・再開（ボタンにフォーカスがあるときは、そのボタンを押します）</dd>
            <dt>
              <kbd>1</kbd>〜<kbd>4</kbd>
            </dt>
            <dd>船を選ぶ（フェリー操作パネルの左から順）</dd>
            <dt>
              <kbd>E</kbd>
            </dt>
            <dd>エンジニアモードを開く・閉じる</dd>
          </dl>
        </li>
      </ol>
      <Link className="btn app-back" to={{ name: "title" }}>
        <ArtIcon kind="back" />
        タイトルへ
      </Link>
    </div>
  );
}
