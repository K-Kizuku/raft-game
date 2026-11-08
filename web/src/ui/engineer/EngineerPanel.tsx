// ゲーム画面に重ねるスロット「エンジニアパネル」（T-407・FR-ENG-01〜03・FR-MAIL-05・ゲームデザイン §7.4）。
// 通常の画面では隠している Raft の中身を、Raft の用語のまま見せる（ゲームデザイン §8）。
// データは props で受けず、ストア（T-403）から読む（ADR-0010・DesignDoc §13.3）。
// 値は届いたものをそのまま出し、Leader や Commit をここで決めない（ADR-0009）。
// 開いている間だけ、選んでいるノードの詳細（node.detail）を sub.engineer で購読する（DesignDoc §13.4）。
// 中身はタブ（ノード一覧・Raft ログ・Progress・手紙）に分け、パネルの中を巻き取らずに収める（T-438）。
// 置き場所はゲーム画面が決める（開いている間は右の島の詳細の列。GameScreen.css）。
import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, ReactElement } from "react";

import type { Letter, LetterEntry } from "../../protocol/index.ts";
import { gameStore, lettersAtPort, lettersOnFerry, useGameStore } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { engineerModeStore } from "../../state/engineerMode.ts";
import { formatDuration } from "../../app/format.ts";
import { LetterIcon } from "../letters/LetterIcon.tsx";
import { isEngineerToggleKey } from "./engineerKey.ts";
import {
  ENGINEER_LETTER_LIMIT,
  findLetter,
  letterFields,
  letterSummary,
  locText,
  nodeName,
  nodeRows,
  targetNodeId,
} from "./engineerView.ts";
import type { FieldRow, LetterLookup } from "./engineerView.ts";
import { ENGINEER_TABS, tabAfterKey } from "./engineerTabs.ts";
import type { EngineerTabId } from "./engineerTabs.ts";
import { ProgressDetailSection, RaftLogSection } from "./NodeDetailSections.tsx";
import { useEngineerSubscriber } from "./EngineerSubscription.tsx";
import type { EngineerSubscriber } from "./EngineerSubscription.tsx";
import "./EngineerPanel.css";

const PANEL_ID = "engineer-panel";
const TABPANEL_ID = "engineer-tabpanel";
const NONE = "—";

function tabId(id: EngineerTabId): string {
  return `engineer-tab-${id}`;
}

export function EngineerPanel(): ReactElement {
  const [open, setOpen] = useState(false);
  // 選んでいるタブ（閉じて開き直しても同じタブ）。
  const [tab, setTab] = useState<EngineerTabId>("nodes");
  const toggleRef = useRef<HTMLButtonElement>(null);
  const world = useGameStore((s) => s.world);
  const selectedIslandId = useGameStore((s) => s.selection.islandId);
  const subscribe = useEngineerSubscriber();
  const targetId = useMemo(() => targetNodeId(world, selectedIslandId), [world, selectedIslandId]);
  const subscribedId = open ? targetId : null;
  useEngineerSubscription(subscribedId, subscribe);

  // 開閉をマップ（React の外）が読める外部ストアに写す。ゲーム画面を離れたら閉じた状態に戻す（T-712）。
  useEffect(() => {
    engineerModeStore.getState().setOpen(open);
    return () => {
      engineerModeStore.getState().setOpen(false);
    };
  }, [open]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent): void {
      if (isEngineerToggleKey(event)) setOpen((value) => !value);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  function close(): void {
    setOpen(false);
    toggleRef.current?.focus();
  }

  return (
    <>
      <button
        ref={toggleRef}
        type="button"
        className="btn btn-paper engineer-toggle"
        aria-expanded={open}
        aria-controls={PANEL_ID}
        aria-keyshortcuts="E"
        onClick={() => {
          setOpen((value) => !value);
        }}
      >
        E エンジニアモード
      </button>
      {open && (
        <aside id={PANEL_ID} aria-label="エンジニアパネル" className="engineer-panel">
          <div className="engineer-header">
            <h2>エンジニアモード</h2>
            <button type="button" className="btn btn-quiet" onClick={close}>
              閉じる
            </button>
          </div>
          {world === null ? (
            <p>まだ島の状態が届いていません。</p>
          ) : (
            <EngineerBody world={world} targetId={targetId} tab={tab} onTab={setTab} />
          )}
        </aside>
      )}
    </>
  );
}

// 購読する ID が変わるたびに sub.engineer を送る。null → ID・ID → 別の ID は enabled: true、
// ID → null は enabled: false（間に enabled: false を挟まない）。購読をやめたら古い詳細を消す。
function useEngineerSubscription(
  subscribedId: number | null,
  subscribe: EngineerSubscriber | null,
): void {
  const sentRef = useRef<number | null>(null);
  const subscribeRef = useRef(subscribe);

  useEffect(() => {
    subscribeRef.current = subscribe;
  }, [subscribe]);

  useEffect(() => {
    if (sentRef.current === subscribedId) return;
    sentRef.current = subscribedId;
    if (subscribedId === null) {
      subscribe?.({ enabled: false });
      gameStore.getState().setNodeDetail(null);
    } else {
      subscribe?.({ enabled: true, nodeId: subscribedId });
    }
  }, [subscribedId, subscribe]);

  useEffect(
    () => () => {
      if (sentRef.current === null) return;
      sentRef.current = null;
      subscribeRef.current?.({ enabled: false });
      gameStore.getState().setNodeDetail(null);
    },
    [],
  );
}

// 島の色の丸（飾り）。色はステージ定義のもの。無ければ CSS の既定（--color-land）。
function islandDotStyle(world: World, id: number): { backgroundColor: string } | undefined {
  const color = world.stage.islands[id]?.color;
  return color === undefined ? undefined : { backgroundColor: color };
}

function idText(id: number | null): string {
  return id === null || id === 0 ? NONE : String(id);
}

function EngineerBody({
  world,
  targetId,
  tab,
  onTab,
}: {
  world: World;
  targetId: number | null;
  tab: EngineerTabId;
  onTab: (tab: EngineerTabId) => void;
}): ReactElement {
  const [letterId, setLetterId] = useState<number | null>(null);

  return (
    <div className="engineer-body">
      <EngineerTabList tab={tab} onTab={onTab} />
      <div
        id={TABPANEL_ID}
        role="tabpanel"
        aria-labelledby={tabId(tab)}
        className="engineer-tabpanel"
        data-tab={tab}
      >
        {tab === "nodes" && <NodeListSection world={world} targetId={targetId} />}
        {tab === "log" && <RaftLogSection world={world} targetId={targetId} />}
        {tab === "progress" && <ProgressDetailSection world={world} targetId={targetId} />}
        {tab === "letters" && (
          <LettersSection world={world} letterId={letterId} onSelect={setLetterId} />
        )}
      </div>
    </div>
  );
}

// タブの列（WAI-ARIA の tablist）。選んでいるタブだけを Tab の順に入れ、← → でタブを移る。
function EngineerTabList({
  tab,
  onTab,
}: {
  tab: EngineerTabId;
  onTab: (tab: EngineerTabId) => void;
}): ReactElement {
  const refs = useRef(new Map<EngineerTabId, HTMLButtonElement>());

  function onKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    const next = tabAfterKey(tab, event.key);
    if (next === null) return;
    event.preventDefault();
    onTab(next);
    refs.current.get(next)?.focus();
  }

  return (
    <div
      role="tablist"
      aria-label="エンジニアパネルの表示"
      className="engineer-tabs"
      onKeyDown={onKeyDown}
    >
      {ENGINEER_TABS.map((t) => (
        <button
          key={t.id}
          ref={(el) => {
            if (el === null) refs.current.delete(t.id);
            else refs.current.set(t.id, el);
          }}
          id={tabId(t.id)}
          type="button"
          role="tab"
          className="engineer-tab"
          aria-selected={t.id === tab}
          aria-controls={TABPANEL_ID}
          tabIndex={t.id === tab ? 0 : -1}
          onClick={() => {
            onTab(t.id);
          }}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

function NodeListSection({
  world,
  targetId,
}: {
  world: World;
  targetId: number | null;
}): ReactElement {
  const rows = useMemo(() => nodeRows(world, targetId), [world, targetId]);
  return (
    <section className="engineer-section">
      <h3>ノード一覧</h3>
      {rows.length === 0 ? (
        <p>ノードの状態が届いていません。</p>
      ) : (
        <table aria-label="ノード一覧" className="engineer-table">
          <thead>
            <tr>
              {["Node", "Role", "Term", "Vote", "Lead", "Commit", "Applied", "Last Index"].map(
                (name) => (
                  <th key={name} scope="col">
                    {name}
                  </th>
                ),
              )}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                key={row.id}
                data-node={row.id}
                data-role={row.role === "leader" ? "leader" : undefined}
              >
                <td>
                  <button
                    type="button"
                    className="engineer-node"
                    aria-pressed={row.target}
                    onClick={() => {
                      gameStore.getState().selectIsland(row.id);
                    }}
                  >
                    <span
                      className="engineer-node-dot"
                      aria-hidden="true"
                      style={islandDotStyle(world, row.id)}
                    />
                    {row.id} {row.name}
                  </button>
                </td>
                <td className={`engineer-role engineer-role--${row.role}`}>{row.roleLabel}</td>
                <td>{row.term}</td>
                <td>{idText(row.vote)}</td>
                <td>{idText(row.lead)}</td>
                <td>{row.commit}</td>
                <td>{row.applied}</td>
                <td>{row.lastIndex}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function LettersSection({
  world,
  letterId,
  onSelect,
}: {
  world: World;
  letterId: number | null;
  onSelect: (id: number) => void;
}): ReactElement {
  const selection = useGameStore((s) => s.selection);
  const lists = useMemo(() => letterLists(world, selection), [world, selection]);
  const lookup = useMemo(
    () => (letterId === null ? null : findLetter(world, letterId)),
    [world, letterId],
  );
  return (
    <>
      <section className="engineer-section">
        <h3>手紙</h3>
        {lists.length === 0 ? (
          <p>マップで島か船を選ぶと、そこにある手紙が並びます。</p>
        ) : (
          lists.map((list) => (
            <LetterList
              key={list.key}
              world={world}
              title={list.title}
              letters={list.letters}
              selectedId={letterId}
              onSelect={onSelect}
            />
          ))
        )}
      </section>
      {letterId !== null && lookup !== null && (
        <LetterDetail world={world} letterId={letterId} lookup={lookup} />
      )}
    </>
  );
}

type LetterListModel = { key: string; title: string; letters: Letter[] };

function letterLists(world: World, selection: UiSelection): LetterListModel[] {
  const lists: LetterListModel[] = [];
  if (selection.islandId !== null) {
    lists.push({
      key: "port",
      title: `港: ${nodeName(world, selection.islandId)}`,
      letters: lettersAtPort(world, selection.islandId),
    });
  }
  if (selection.ferryId !== null) {
    lists.push({
      key: "ferry",
      title: `船: ${world.ferries[selection.ferryId]?.name ?? selection.ferryId}`,
      letters: lettersOnFerry(world, selection.ferryId),
    });
  }
  return lists;
}

function LetterList({
  world,
  title,
  letters,
  selectedId,
  onSelect,
}: {
  world: World;
  title: string;
  letters: readonly Letter[];
  selectedId: number | null;
  onSelect: (id: number) => void;
}): ReactElement {
  const shown = letters.slice(0, ENGINEER_LETTER_LIMIT);
  const rest = letters.length - shown.length;
  return (
    <div className="engineer-letters">
      <h4>{title}</h4>
      {letters.length === 0 ? (
        <p>手紙はありません。</p>
      ) : (
        <ul>
          {shown.map((letter) => {
            const summary = letterSummary(letter);
            return (
              <li key={letter.id}>
                <button
                  type="button"
                  aria-pressed={letter.id === selectedId}
                  aria-label={summary}
                  data-letter={letter.id}
                  onClick={() => {
                    onSelect(letter.id);
                  }}
                >
                  <LetterIcon letter={letter} borderColor={world.stage.islands[letter.to]?.color} />
                  <span>{summary}</span>
                  {letter.raft?.stale === true && (
                    <span className="engineer-stale" data-stale="true">
                      stale
                    </span>
                  )}
                </button>
              </li>
            );
          })}
          {rest > 0 && <li className="engineer-more">+{rest} 通</li>}
        </ul>
      )}
    </div>
  );
}

function dropReasonText(reason: string): string {
  switch (reason) {
    case "superseded":
      return "新しい手紙に差し替え";
    case "overflow":
      return "港があふれた";
    default:
      return reason;
  }
}

function lookupStatusText(world: World, lookup: LetterLookup): string {
  switch (lookup.status) {
    case "present":
      return locText(world, lookup.letter.loc);
    case "delivered":
      return `配達済み（${formatDuration(lookup.t)}、${nodeName(world, lookup.islandId)}、${
        lookup.accepted ? "受け取り" : "無視（古い・重複）"
      }）`;
    case "dropped":
      return `処分（${formatDuration(lookup.t)}、${dropReasonText(lookup.reason)}）`;
    case "missing":
      return "この手紙はもうありません。";
  }
}

function LetterDetail({
  world,
  letterId,
  lookup,
}: {
  world: World;
  letterId: number;
  lookup: LetterLookup;
}): ReactElement {
  const letter = lookup.status === "missing" ? null : lookup.letter;
  return (
    <section className="engineer-section engineer-letter-detail">
      <h3>手紙の詳細 #{letterId}</h3>
      <p className="engineer-loc">{lookupStatusText(world, lookup)}</p>
      {letter !== null && (
        <>
          {letter.raft?.stale === true && (
            <p className="engineer-stale-note">
              古い手紙（送り主の今の Term より古い Term の手紙）
            </p>
          )}
          <Fields fields={letterFields(letter)} />
          {letter.kind !== "request" && letter.raft !== undefined && (
            <Entries entries={letter.raft.entries} />
          )}
        </>
      )}
    </section>
  );
}

function Fields({ fields }: { fields: readonly FieldRow[] }): ReactElement {
  return (
    <dl className="engineer-fields">
      {fields.map((field) => (
        <div key={field.name}>
          <dt>{field.name}</dt>
          <dd className={field.name === "Type" ? "engineer-msg-type" : undefined}>{field.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Entries({ entries }: { entries: readonly LetterEntry[] }): ReactElement {
  if (entries.length === 0) return <p>Entries はありません。</p>;
  return (
    <table aria-label="Entries" className="engineer-table">
      <thead>
        <tr>
          {["Index", "Term", "Type", "Summary"].map((name) => (
            <th key={name} scope="col">
              {name}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {entries.map((entry, i) => (
          <tr key={`${String(entry.index)}-${String(i)}`}>
            <td>{entry.index}</td>
            <td>{entry.term}</td>
            <td>{entry.type}</td>
            <td>{entry.summary}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
