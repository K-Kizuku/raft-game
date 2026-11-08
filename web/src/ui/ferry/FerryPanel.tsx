// ゲーム画面のスロット「フェリー操作」（T-405・ゲームデザイン §3.2・§7.2 の下・DesignDoc §13.8）。
// 船を選ぶ・港の手紙を積む／降ろす・中継倉庫に預ける／倉庫から積む（クリック・キーボード・ドラッグ＆ドロップ。T-722）・行き先・おまかせ・出航。
// 船が 2 隻以上なら、選んだ船を自動航行（ボット）に切り替えられる（T-725。features.autoFerry が false なら出さない）。
// 表示はストア（T-403）に届いた値だけで変え、コマンドを送っただけでは変えない（ADR-0009）。
// 成否はサーバーの ack で知る。押せなくするのは「送っても明らかに失敗する」ときだけ。
import {
  DndContext,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import type { Announcements, DragEndEvent } from "@dnd-kit/core";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactElement, ReactNode } from "react";

import type { Ack, Letter } from "../../protocol/index.ts";
import { gameStore, useGameStore } from "../../state/index.ts";
import type { UiSelection, World } from "../../state/index.ts";
import { useGameCommand } from "../commands/GameCommands.tsx";
import type { GameCommandSender } from "../commands/GameCommands.tsx";
import { formatRemaining } from "../hud/format.ts";
import { useSimNow } from "../hud/useSimNow.ts";
import { LetterIcon } from "../letters/LetterIcon.tsx";
import { letterKind } from "../letters/letterKinds.ts";
import { letterRaftTerm, withRaftTerm } from "../terms/glossary.ts";
import { useShowRaftTerms } from "../terms/preference.ts";
import { RaftTerm } from "../terms/RaftTerm.tsx";
import { sendFerryAuto, useAutoFerryFeature } from "./autoFerryFeature.ts";
import { CARGO_DROP_ID, PORT_DROP_ID, RELAY_DROP_ID, dragId, dropCommand } from "./dnd.ts";
import type { LetterDragData } from "./dnd.ts";
import { FerryList } from "./FerryList.tsx";
import {
  AUTO_FERRY_BUSY,
  autoFerryResultMessage,
  blockerText,
  ferryResultMessage,
  viaHintText,
  viaLabel,
} from "./ferryMessages.ts";
import type { FerryOp } from "./ferryMessages.ts";
import { autoloadBlocker, departBlocker, ferryDetail, ferryTabs } from "./ferryView.ts";
import type { FerryDetail, FerryTab, RelayGroup } from "./ferryView.ts";
import { onIslandPress } from "./islandPress.ts";
import "./FerryPanel.css";

const RELAY_NOTE = "預けた手紙は、宛先に着くまで島では読まれません";
const DESTINATION_HOW_TO = "下のボタンか、マップで隣の島を押して選びます";
const AUTO_NOTE = "ボットが積み込みと出航をします。手では操作できません。";
const SCREEN_READER_INSTRUCTIONS =
  "Enter か Space で積み込み・荷降ろしをします。マウスではドラッグでもできます。";

type LetterList = "port" | "cargo" | "relay";
// 行き先（パネルの状態。ストアに入れない）。船と島が今と同じときだけ使う。
type DestinationChoice = Readonly<{ ferryId: string; islandId: number; to: number }>;
// 結果の文（選んだ船ごと）。
type StatusText = Readonly<{ ferryId: string; text: string }>;
// ok で返ったのにイベントがまだ届いていない手紙（locType は送ったときの loc.type）。
type PendingLetter = Readonly<{ letterId: number; locType: Letter["loc"]["type"] }>;
// 手紙のボタンで積む・降ろしたとき、その手紙が一覧から消えたらフォーカスを移す先。
type FocusIntent = Readonly<{ ferryId: string; list: LetterList; letterId: number; index: number }>;

export function FerryPanel(): ReactElement {
  const world = useGameStore((s) => s.world);
  const selection = useGameStore((s) => s.selection);
  if (world === null) {
    return <p>フェリー操作（準備中）</p>;
  }
  return <FerryPanelContent world={world} selection={selection} />;
}

function selectFerry(id: string): void {
  gameStore.getState().selectFerry(id);
}

function FerryStateText({
  state,
  placeName,
  etaMs,
}: {
  state: "docked" | "sailing";
  placeName: string | null;
  etaMs: number | null;
}): ReactElement {
  if (state === "docked") {
    if (placeName === null) return <>停泊中</>;
    return (
      <>
        <span aria-hidden="true">⚓</span>
        {` ${placeName} 停泊中`}
      </>
    );
  }
  if (placeName === null) return <>航行中</>;
  return <>{`→ ${placeName} あと ${formatRemaining(etaMs ?? 0)}`}</>;
}

function BrokenMark(): ReactElement {
  return (
    <span className="ferry-panel__broken">
      <span aria-hidden="true">🔧</span> 故障中
    </span>
  );
}

// 自動航行の船の印（T-725）。色だけでなく文字でも示す。
function AutoMark(): ReactElement {
  return (
    <span className="ferry-panel__auto-mark">
      <span aria-hidden="true">🧭</span> 自動
    </span>
  );
}

function FerryTabs({ tabs }: { tabs: readonly FerryTab[] }): ReactElement {
  if (tabs.length === 0) {
    return <p className="ferry-panel__note">船がありません。</p>;
  }
  return (
    <div role="group" aria-label="船を選ぶ" className="ferry-panel__tabs">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          className="ferry-panel__tab"
          aria-pressed={tab.selected}
          data-ferry={tab.id}
          onClick={() => {
            selectFerry(tab.id);
          }}
        >
          <span className="ferry-panel__tab-name">
            <span aria-hidden="true">⛴</span>
            {` ${tab.name}`}
          </span>{" "}
          <span className="ferry-panel__tab-state">
            <FerryStateText state={tab.state} placeName={tab.placeName} etaMs={tab.etaMs} />
          </span>{" "}
          <span className="ferry-panel__tab-cargo">{`積み荷 ${String(tab.cargoCount)}/${String(tab.capacity)}`}</span>
          {tab.auto && (
            <>
              {" "}
              <AutoMark />
            </>
          )}
          {tab.broken && (
            <>
              {" "}
              <BrokenMark />
            </>
          )}
        </button>
      ))}
    </div>
  );
}

function FerryPanelContent({
  world,
  selection,
}: {
  world: World;
  selection: UiSelection;
}): ReactElement {
  const simNow = useSimNow() ?? world.clock.simTime;
  const selectedId = selection.ferryId;
  const tabs = useMemo(() => ferryTabs(world, selection, simNow), [world, selection, simNow]);
  const detail = useMemo(
    () => (selectedId === null ? null : ferryDetail(world, selectedId, simNow)),
    [world, selectedId, simNow],
  );

  return (
    <div className="ferry-panel">
      {detail === null ? (
        // 何も選んでいないときは、船のタブの代わりに船の一覧（T-426）を出す。押すとその船を選ぶ。
        <>
          <p className="ferry-panel__note">船を選ぶと、積み込みと出航の操作がここに出ます。</p>
          <FerryList tabs={tabs} />
        </>
      ) : (
        <>
          <FerryTabs tabs={tabs} />
          <FerryOperations world={world} detail={detail} />
        </>
      )}
    </div>
  );
}

function letterIdsOf(detail: FerryDetail, list: LetterList): number[] {
  if (list === "port") return detail.port.flatMap((group) => group.letters.map((l) => l.id));
  if (list === "relay") return detail.relay.flatMap((group) => group.letters.map((l) => l.id));
  return detail.cargo.flatMap((slot) => (slot.kind === "letter" ? [slot.letter.id] : []));
}

function letterButtons(list: HTMLElement | null): HTMLButtonElement[] {
  if (list === null) return [];
  return [...list.querySelectorAll<HTMLButtonElement>("button[data-letter-id]")];
}

function isStillAt(world: World | null, entry: PendingLetter): boolean {
  if (world === null) return false;
  const letter = Object.hasOwn(world.letters, entry.letterId)
    ? world.letters[entry.letterId]
    : undefined;
  return letter !== undefined && letter.loc.type === entry.locType;
}

function FerryOperations({ world, detail }: { world: World; detail: FerryDetail }): ReactElement {
  const ferryId = detail.ferry.id;
  const send = useGameCommand();
  const show = useShowRaftTerms();
  const ids = useId();
  const [sending, setSending] = useState(false);
  const [status, setStatus] = useState<StatusText | null>(null);
  const [pending, setPending] = useState<readonly PendingLetter[]>([]);
  const [destination, setDestination] = useState<DestinationChoice | null>(null);
  const [shownFerry, setShownFerry] = useState(ferryId);

  const mountedRef = useRef(false);
  const sendingRef = useRef(false);
  const focusIntentRef = useRef<FocusIntent | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const portListRef = useRef<HTMLElement | null>(null);
  const cargoListRef = useRef<HTMLElement | null>(null);
  const relayListRef = useRef<HTMLElement | null>(null);
  const portHeadingRef = useRef<HTMLHeadingElement | null>(null);
  const cargoHeadingRef = useRef<HTMLHeadingElement | null>(null);
  const relayHeadingRef = useRef<HTMLHeadingElement | null>(null);
  // 一覧ごとの要素と見出し（フォーカスの移し先）。中身は ref なので、描画をまたいで同じもの。
  const listRefs = useMemo(
    () => ({
      port: { list: portListRef, heading: portHeadingRef },
      cargo: { list: cargoListRef, heading: cargoHeadingRef },
      relay: { list: relayListRef, heading: relayHeadingRef },
    }),
    [],
  );

  const autoFeature = useAutoFerryFeature();
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));

  // 選んだ船が変わったら、結果の文・反映待ち・行き先を捨てる（描画の中で前の値と比べて直す）。
  if (shownFerry !== ferryId) {
    setShownFerry(ferryId);
    setStatus(null);
    setPending([]);
    setDestination(null);
  }

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // 手紙のボタンで積む・降ろした手紙が一覧から消えたら、同じ一覧の同じ位置（無ければ 1 つ前、
  // それも無ければ見出し）にフォーカスを移す。フォーカスがパネルの外にあるときは動かさない。
  useLayoutEffect(() => {
    const intent = focusIntentRef.current;
    if (intent === null) return;
    if (intent.ferryId !== ferryId) {
      focusIntentRef.current = null;
      return;
    }
    if (letterIdsOf(detail, intent.list).includes(intent.letterId)) return;
    focusIntentRef.current = null;
    const root = rootRef.current;
    const active = document.activeElement;
    if (root === null) return;
    if (active !== null && active !== document.body && !root.contains(active)) return;
    const refs = listRefs[intent.list];
    const heading = refs.heading.current;
    const buttons = letterButtons(refs.list.current);
    const target =
      buttons.length > 0 ? buttons[Math.min(intent.index, buttons.length - 1)] : heading;
    target?.focus();
  }, [detail, ferryId, listRefs]);

  // マップで隣の島を押したら、その島を行き先にする（#318。港にいるときだけ。隣でない島は何もしない）。
  const detailRef = useRef(detail);
  useLayoutEffect(() => {
    detailRef.current = detail;
  }, [detail]);
  useEffect(
    () =>
      onIslandPress((islandId) => {
        const d = detailRef.current;
        if (d.state !== "docked" || d.islandId === null) return;
        if (!d.destinations.some((x) => x.islandId === islandId)) return;
        setDestination({ ferryId: d.ferry.id, islandId: d.islandId, to: islandId });
      }),
    [],
  );

  const setPortList = useCallback((el: HTMLElement | null) => {
    portListRef.current = el;
  }, []);
  const setCargoList = useCallback((el: HTMLElement | null) => {
    cargoListRef.current = el;
  }, []);
  const setRelayList = useCallback((el: HTMLElement | null) => {
    relayListRef.current = el;
  }, []);

  const docked = detail.state === "docked";
  const destinationTo =
    destination !== null &&
    docked &&
    destination.ferryId === ferryId &&
    destination.islandId === detail.islandId &&
    detail.destinations.some((d) => d.islandId === destination.to)
      ? destination.to
      : null;
  const destinationName =
    detail.destinations.find((d) => d.islandId === destinationTo)?.name ?? null;
  const phaseOk = world.phase === "sailing";
  const canOperate = send !== null && phaseOk && !sending;
  // 自動航行（T-725）。自動の船は手での積み込み・出航を受け付けない（押すと「自動航行中です」）。
  // 切り替えは船が 2 隻以上で、スイッチ（features.autoFerry）が有効なときだけ出す。Intro・一時停止中も切り替えられる。
  const auto = detail.auto;
  const showAutoToggle = autoFeature && world.ferryIds.length >= 2;
  const canToggleAuto = send !== null && world.phase !== "ended" && !sending;
  const showAutoBusy = (): void => {
    setStatus({ ferryId, text: AUTO_FERRY_BUSY });
  };
  const shownPending = shownFerry === ferryId ? pending : [];
  const isPending = (letter: Letter): boolean =>
    shownPending.some((p) => p.letterId === letter.id && p.locType === letter.loc.type);

  // message は結果の文。onOk は ok の ack が返ったときに呼ぶ（パネルが消えていても呼ぶ）。
  function dispatch(
    message: (result: Ack | "rejected") => string,
    request: (sender: GameCommandSender) => Promise<Ack>,
    moved: PendingLetter | null,
    onOk?: () => void,
  ): void {
    if (send === null || sendingRef.current) return;
    sendingRef.current = true;
    setSending(true);
    let promise: Promise<Ack>;
    try {
      promise = request(send);
    } catch (error: unknown) {
      promise = Promise.reject(error instanceof Error ? error : new Error(String(error)));
    }
    promise
      .then(
        (ack): Ack | "rejected" => ack,
        (): "rejected" => "rejected",
      )
      .then((result) => {
        sendingRef.current = false;
        const ok = result !== "rejected" && result.ok;
        if (ok) onOk?.();
        if (!mountedRef.current) return;
        setSending(false);
        setStatus({ ferryId, text: message(result) });
        if (!ok) {
          if (focusIntentRef.current?.letterId === moved?.letterId) focusIntentRef.current = null;
          return;
        }
        if (moved === null) return;
        const state = gameStore.getState();
        if (state.selection.ferryId !== ferryId) return;
        setPending((prev) => [
          ...prev.filter((p) => p.letterId !== moved.letterId && isStillAt(state.world, p)),
          moved,
        ]);
      });
  }

  // op は押した・落とした操作。unload は積み荷の降ろし先（unloadTo）で、港へ戻す（unload）か
  // 倉庫に預ける（deposit）かを分ける（送るコマンドはどちらも cmd.ferry.unload。結果の文だけが違う）。
  function sendLetter(
    op: "load" | "unload" | "relayLoad",
    letterId: number,
    fromButton: boolean,
  ): void {
    if (!canOperate || !docked || auto) return;
    const letter = Object.hasOwn(world.letters, letterId) ? world.letters[letterId] : undefined;
    if (letter === undefined || isPending(letter)) return;
    let list: LetterList;
    let resultOp: FerryOp;
    if (op === "load" || op === "relayLoad") {
      list = op === "load" ? "port" : "relay";
      resultOp = op;
      if (!letterIdsOf(detail, list).includes(letterId)) return;
    } else {
      const slot = detail.cargo.find(
        (s) => s.kind === "letter" && s.letter.id === letterId && s.unloadable,
      );
      if (slot?.kind !== "letter") return;
      list = "cargo";
      resultOp = slot.unloadTo === "relay" ? "deposit" : "unload";
    }
    if (fromButton) {
      const index = letterButtons(listRefs[list].list.current).findIndex(
        (b) => b.dataset["letterId"] === String(letterId),
      );
      focusIntentRef.current = { ferryId, list, letterId, index: Math.max(0, index) };
    } else {
      focusIntentRef.current = null;
    }
    const moved: PendingLetter = { letterId, locType: letter.loc.type };
    dispatch(
      (result) => ferryResultMessage(resultOp, result, destinationName),
      (sender) => {
        const payload = { ferryId, letterIds: [letterId] };
        if (op === "load") return sender("cmd.ferry.load", payload);
        if (op === "relayLoad") return sender("cmd.relay.load", payload);
        return sender("cmd.ferry.unload", payload);
      },
      moved,
    );
  }

  const autoload = autoloadBlocker(detail, destinationTo);
  const depart = departBlocker(detail, destinationTo);

  function toggleAuto(): void {
    if (!canToggleAuto) return;
    const enabled = !auto;
    focusIntentRef.current = null;
    dispatch(
      (result) => autoFerryResultMessage(enabled, result),
      (sender) => sendFerryAuto(sender, { ferryId, enabled }),
      null,
      () => {
        gameStore.getState().setAutoFerry(ferryId, enabled);
      },
    );
  }

  function sendTo(op: "autoload" | "depart"): void {
    const blocker = op === "autoload" ? autoload : depart;
    if (!canOperate || auto || blocker !== null || destinationTo === null) return;
    const to = destinationTo;
    focusIntentRef.current = null;
    dispatch(
      (result) => ferryResultMessage(op, result, destinationName),
      (sender) =>
        op === "autoload"
          ? sender("cmd.ferry.autoload", { ferryId, to })
          : sender("cmd.ferry.depart", { ferryId, to }),
      null,
    );
  }

  // ドラッグの data（LetterDragData）から手紙の名前を出す。分からなければ「手紙」。
  const letterName = (data: unknown): string => {
    const id: unknown =
      typeof data === "object" && data !== null ? Reflect.get(data, "letterId") : undefined;
    if (typeof id !== "number") return "手紙";
    const letter = Object.hasOwn(world.letters, id) ? world.letters[id] : undefined;
    if (letter === undefined) return "手紙";
    return withRaftTerm(letterKind(letter.label).name, letterRaftTerm(letter.label), show);
  };
  const announcements: Announcements = {
    onDragStart: ({ active }) => `${letterName(active.data.current)}を持ち上げました。`,
    onDragOver: ({ over }) => {
      if (over?.id === CARGO_DROP_ID) return "積み荷の上です。";
      if (over?.id === PORT_DROP_ID) return "港の上です。";
      if (over?.id === RELAY_DROP_ID) return "倉庫の上です。";
      return undefined;
    },
    onDragEnd: ({ active }) => `${letterName(active.data.current)}を置きました。`,
    onDragCancel: ({ active }) => `${letterName(active.data.current)}のドラッグをやめました。`,
  };

  function onDragEnd({ active, over }: DragEndEvent): void {
    const command = dropCommand(ferryId, active.data.current, over?.id ?? null);
    if (command === null) return;
    sendLetter(command.op, command.letterIds[0], false);
  }

  const phaseNoteId = `${ids}-phase`;
  const autoNoteId = `${ids}-auto`;
  const statusText = status !== null && status.ferryId === ferryId ? status.text : "";
  const cargoLetters = detail.ferry.cargo.length;

  const letterProps = (
    letter: Letter,
    source: LetterDragData["source"],
    toName: string,
    toColor: string,
    unloadable = true,
    unloadNote: string | null = null,
  ): LetterButtonProps => {
    const waiting = isPending(letter);
    const blocked = source === "cargo" && !unloadable;
    const disabled = !canOperate || auto || waiting || blocked;
    const blockedNote = blocked ? unloadNote : null;
    return {
      letter,
      source,
      toName,
      toColor,
      disabled,
      note: blockedNote ?? (waiting ? "反映待ち" : null),
      noteVisible: blockedNote === null && waiting,
      title: blockedNote ?? undefined,
      describedBy: auto ? autoNoteId : phaseOk ? undefined : phaseNoteId,
      onBlocked: auto ? showAutoBusy : undefined,
      onActivate: () => {
        sendLetter(
          source === "port" ? "load" : source === "relay" ? "relayLoad" : "unload",
          letter.id,
          true,
        );
      },
    };
  };

  return (
    <div className="ferry-panel__detail" ref={rootRef} data-ferry={ferryId}>
      <div className="ferry-panel__head">
        <h2 className="ferry-panel__title">
          {`${detail.name} `}
          <span className="ferry-panel__title-state">
            <FerryStateText
              state={detail.state}
              placeName={docked ? detail.islandName : detail.toName}
              etaMs={detail.etaMs}
            />
          </span>
          {auto && (
            <>
              {" "}
              <AutoMark />
            </>
          )}
          {detail.broken && (
            <>
              {" "}
              <BrokenMark />
            </>
          )}
        </h2>
        {showAutoToggle && (
          <AutoToggle
            on={auto}
            disabled={!canToggleAuto}
            onToggle={() => {
              toggleAuto();
            }}
          />
        )}
      </div>
      {auto && (
        // 行を増やさない（1470×860 でスクロールしない）よう、見た目は名前の行の印とボタンの横の理由で示し、
        // 説明は読み上げにだけ出す（ボタンの aria-describedby）。
        <span className="visually-hidden" id={autoNoteId}>
          {AUTO_NOTE}
        </span>
      )}
      {docked && detail.readyInMs > 0 && (
        <p className="ferry-panel__ready">{`出航まで あと ${formatRemaining(detail.readyInMs)}`}</p>
      )}
      {!phaseOk && (
        <p className="ferry-panel__note" id={phaseNoteId}>
          {world.phase === "ended"
            ? "ラウンドは終わりました。"
            : "航行の時間になると操作できます。"}
        </p>
      )}

      <div className="ferry-panel__areas">
        <DndContext
          sensors={sensors}
          onDragEnd={onDragEnd}
          accessibility={{
            announcements,
            screenReaderInstructions: { draggable: SCREEN_READER_INSTRUCTIONS },
          }}
        >
          {docked && (
            <section className="ferry-panel__section">
              <h3 tabIndex={-1} ref={portHeadingRef}>
                {detail.islandName === null ? "港の" : `${detail.islandName}の港の`}
                <RaftTerm id="letter">手紙</RaftTerm>
              </h3>
              <DropArea id={PORT_DROP_ID} className="ferry-panel__port" nodeRef={setPortList}>
                {detail.port.length === 0 ? (
                  <p className="ferry-panel__note">港に手紙はありません。</p>
                ) : (
                  <ul className="ferry-panel__piles">
                    {detail.port.map((group) => (
                      <li key={group.to} className="ferry-panel__pile" data-to={group.to}>
                        <h4 className="ferry-panel__pile-to">
                          {`→ ${group.toName}`}
                          {destinationTo === group.to && (
                            <span className="ferry-panel__pile-target">行き先宛て</span>
                          )}
                          {destinationTo !== null &&
                            detail.viaHints.some(
                              (h) => h.to === group.to && h.via === destinationTo,
                            ) && <span className="ferry-panel__pile-target">行き先経由</span>}
                        </h4>
                        <ul className="ferry-panel__letters">
                          {group.letters.map((letter) => (
                            <li key={letter.id}>
                              <LetterButton
                                {...letterProps(letter, "port", group.toName, group.toColor)}
                              />
                            </li>
                          ))}
                        </ul>
                      </li>
                    ))}
                  </ul>
                )}
              </DropArea>
            </section>
          )}

          {docked && detail.relayCap > 0 && (
            <section className="ferry-panel__section ferry-panel__relay-section">
              <h3 tabIndex={-1} ref={relayHeadingRef}>
                <span aria-hidden="true">📦</span>
                {` ${detail.islandName === null ? "倉庫" : `${detail.islandName}の倉庫`} ${String(detail.relayCount)}/${String(detail.relayCap)}`}
              </h3>
              <p className="ferry-panel__note">{RELAY_NOTE}</p>
              <DropArea id={RELAY_DROP_ID} className="ferry-panel__relay" nodeRef={setRelayList}>
                {detail.relay.length === 0 ? (
                  <p className="ferry-panel__note">倉庫に手紙はありません。</p>
                ) : (
                  <ul className="ferry-panel__piles">
                    {detail.relay.map((group: RelayGroup) => (
                      <li key={group.to} className="ferry-panel__pile" data-to={group.to}>
                        <h4 className="ferry-panel__pile-to">
                          {`→ ${group.toName}`}
                          {destinationTo === group.to && (
                            <span className="ferry-panel__pile-target">行き先宛て</span>
                          )}
                        </h4>
                        <ul className="ferry-panel__letters">
                          {group.letters.map((letter) => (
                            <li key={letter.id}>
                              <LetterButton
                                {...letterProps(letter, "relay", group.toName, group.toColor)}
                              />
                            </li>
                          ))}
                        </ul>
                      </li>
                    ))}
                  </ul>
                )}
              </DropArea>
            </section>
          )}

          <section className="ferry-panel__section">
            <h3 tabIndex={-1} ref={cargoHeadingRef}>
              {`積み荷 ${String(cargoLetters)}/${String(detail.ferry.capacity)}`}
            </h3>
            <DropArea
              id={CARGO_DROP_ID}
              className="ferry-panel__cargo"
              nodeRef={setCargoList}
              as="ol"
            >
              {detail.cargo.map((slot, i) => {
                switch (slot.kind) {
                  case "letter":
                    return (
                      <li key={`l${String(slot.letter.id)}`} className="ferry-panel__slot">
                        {docked ? (
                          <LetterButton
                            {...letterProps(
                              slot.letter,
                              "cargo",
                              slot.toName,
                              slot.toColor,
                              slot.unloadable,
                              slot.unloadNote,
                            )}
                            viaName={slot.viaName}
                          />
                        ) : (
                          <span className="ferry-panel__cargo-letter">
                            <LetterIcon letter={slot.letter} borderColor={slot.toColor} />
                            <LetterDestination toName={slot.toName} viaName={slot.viaName} />
                          </span>
                        )}
                      </li>
                    );
                  case "unknown":
                    return (
                      <li
                        key={`u${String(slot.letterId)}`}
                        className="ferry-panel__slot ferry-panel__slot--unknown"
                      >
                        <span aria-hidden="true">？</span> 不明な手紙
                      </li>
                    );
                  case "empty":
                    return (
                      <li
                        key={`e${String(i)}`}
                        className="ferry-panel__slot ferry-panel__slot--empty"
                      >
                        空き
                      </li>
                    );
                }
              })}
            </DropArea>
          </section>
        </DndContext>
      </div>

      {docked ? (
        <>
          <fieldset className="ferry-panel__destinations">
            <legend>行き先</legend>
            <p className="ferry-panel__destination-current">
              {destinationName === null ? (
                <>
                  行き先: <span className="ferry-panel__destination-none">未選択</span>{" "}
                  <span className="ferry-panel__destination-how">{DESTINATION_HOW_TO}</span>
                </>
              ) : (
                <>
                  行き先: <strong>{destinationName}</strong>
                </>
              )}
            </p>
            {detail.destinations.length === 0 ? (
              <p className="ferry-panel__note">行き先がありません。</p>
            ) : (
              detail.destinations.map((d) => (
                <label key={d.islandId} className="ferry-panel__destination">
                  <input
                    type="radio"
                    name={`ferry-destination-${ferryId}`}
                    value={d.islandId}
                    checked={destinationTo === d.islandId}
                    disabled={auto}
                    onChange={() => {
                      if (detail.islandId === null) return;
                      setDestination({ ferryId, islandId: detail.islandId, to: d.islandId });
                    }}
                  />
                  {` ${d.name}`}
                  {d.blocked || d.forecast ? " " : null}
                  {d.blocked ? (
                    <span className="ferry-panel__storm">
                      <span aria-hidden="true">⛈</span> 嵐で通れません
                    </span>
                  ) : d.forecast ? (
                    <span className="ferry-panel__forecast">
                      <span aria-hidden="true">☁</span> 嵐の予報
                    </span>
                  ) : null}
                </label>
              ))
            )}
            {detail.viaHints.length > 0 && (
              <ul className="ferry-panel__via-hints">
                {detail.viaHints.map((hint) => (
                  <li key={hint.to}>{viaHintText(hint)}</li>
                ))}
              </ul>
            )}
          </fieldset>

          <div className="ferry-panel__actions">
            <ActionButton
              className="ferry-panel__autoload"
              label="おまかせ"
              disabled={!canOperate || auto || autoload !== null}
              reason={
                auto
                  ? AUTO_FERRY_BUSY
                  : autoload === null
                    ? null
                    : blockerText(autoload, detail.readyInMs)
              }
              reasonId={`${ids}-autoload`}
              extraDescribedBy={auto ? autoNoteId : phaseOk ? undefined : phaseNoteId}
              onBlocked={auto ? showAutoBusy : undefined}
              onActivate={() => {
                sendTo("autoload");
              }}
            />
            <ActionButton
              className="ferry-panel__depart"
              label="出航"
              disabled={!canOperate || auto || depart !== null}
              reason={
                auto
                  ? AUTO_FERRY_BUSY
                  : depart === null
                    ? null
                    : blockerText(depart, detail.readyInMs)
              }
              reasonId={`${ids}-depart`}
              extraDescribedBy={auto ? autoNoteId : phaseOk ? undefined : phaseNoteId}
              onBlocked={auto ? showAutoBusy : undefined}
              onActivate={() => {
                sendTo("depart");
              }}
            />
          </div>
        </>
      ) : (
        <p className="ferry-panel__note">
          航行中は操作できません。着いたら積み込みと出航ができます。
        </p>
      )}

      <p role="status" className="ferry-panel__status">
        {statusText}
      </p>
    </div>
  );
}

function DropArea({
  id,
  className,
  nodeRef,
  as = "div",
  children,
}: {
  id: string;
  className: string;
  nodeRef: (el: HTMLElement | null) => void;
  as?: "div" | "ol";
  children: ReactNode;
}): ReactElement {
  const { setNodeRef, isOver } = useDroppable({ id });
  const ref = (el: HTMLElement | null): void => {
    setNodeRef(el);
    nodeRef(el);
  };
  const props = {
    className: isOver ? `${className} is-over` : className,
    "data-over": isOver ? "true" : undefined,
  };
  if (as === "ol") {
    return (
      <ol ref={ref} {...props}>
        {children}
      </ol>
    );
  }
  return (
    <div ref={ref} {...props}>
      {children}
    </div>
  );
}

// 手紙の宛先（「→ 小豆島」）。経由して運ぶ手紙は、前に「経由」の札を付ける（「豊島経由 → 小豆島」）。
function LetterDestination({
  toName,
  viaName,
}: {
  toName: string;
  viaName: string | null;
}): ReactElement {
  if (viaName === null) return <span className="ferry-panel__letter-to">{`→ ${toName}`}</span>;
  return (
    <span className="ferry-panel__letter-to">
      <span className="ferry-panel__via">{viaLabel(viaName)}</span>
      {` → ${toName}`}
    </span>
  );
}

type LetterButtonProps = Readonly<{
  letter: Letter;
  source: LetterDragData["source"];
  toName: string;
  toColor: string;
  viaName?: string | null; // 積み荷の経由の手紙なら、経由する島の名前
  disabled: boolean;
  note: string | null; // ボタンを説明する文（aria-describedby）
  noteVisible: boolean;
  title: string | undefined;
  describedBy: string | undefined;
  onBlocked?: (() => void) | undefined; // 押せないときに押したら呼ぶ（自動航行の船。T-725）
  onActivate: () => void;
}>;

function LetterButton({
  letter,
  source,
  toName,
  toColor,
  viaName = null,
  disabled,
  note,
  noteVisible,
  title,
  describedBy,
  onBlocked,
  onActivate,
}: LetterButtonProps): ReactElement {
  const show = useShowRaftTerms();
  const noteId = useId();
  const data: LetterDragData = { letterId: letter.id, source };
  // attributes（aria-roledescription など）は渡さない。ボタンのままの意味にする。
  const { listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: dragId(data),
    data,
    disabled,
  });
  const name = withRaftTerm(letterKind(letter.label).name, letterRaftTerm(letter.label), show);
  const action = source === "cargo" ? "降ろす" : "積む";
  const described = [describedBy, note === null ? undefined : noteId]
    .filter((v) => v !== undefined)
    .join(" ");
  return (
    <>
      <button
        type="button"
        ref={setNodeRef}
        {...listeners}
        className={isDragging ? "ferry-panel__letter is-dragging" : "ferry-panel__letter"}
        data-letter-id={letter.id}
        aria-label={`${name}（${viaName === null ? "" : `${viaLabel(viaName)} `}→ ${toName}）を${action}`}
        aria-disabled={disabled ? "true" : undefined}
        aria-describedby={described === "" ? undefined : described}
        title={title}
        style={
          transform === null
            ? undefined
            : { transform: `translate3d(${String(transform.x)}px, ${String(transform.y)}px, 0)` }
        }
        onClick={() => {
          if (!disabled) onActivate();
          else onBlocked?.();
        }}
      >
        <LetterIcon letter={letter} borderColor={toColor} />
        <LetterDestination toName={toName} viaName={viaName} />
      </button>
      {note !== null && (
        <span id={noteId} className={noteVisible ? "ferry-panel__letter-note" : "visually-hidden"}>
          {note}
        </span>
      )}
    </>
  );
}

function ActionButton({
  className,
  label,
  disabled,
  reason,
  reasonId,
  extraDescribedBy,
  onBlocked,
  onActivate,
}: {
  className: string;
  label: string;
  disabled: boolean;
  reason: string | null;
  reasonId: string;
  extraDescribedBy: string | undefined;
  onBlocked?: (() => void) | undefined; // 押せないときに押したら呼ぶ（自動航行の船。T-725）
  onActivate: () => void;
}): ReactElement {
  const described = [reason === null ? undefined : reasonId, extraDescribedBy]
    .filter((v) => v !== undefined)
    .join(" ");
  return (
    <div className="ferry-panel__action">
      <button
        type="button"
        className={className}
        aria-disabled={disabled ? "true" : undefined}
        aria-describedby={described === "" ? undefined : described}
        onClick={() => {
          if (!disabled) onActivate();
          else onBlocked?.();
        }}
      >
        {label}
      </button>
      {reason !== null && (
        <p id={reasonId} className="ferry-panel__reason">
          {reason}
        </p>
      )}
    </div>
  );
}

// 自動航行（ボット）の切り替え（T-725）。押すたびに cmd.ferry.auto を送る。表示はストアの値（ok の ack の後）で変わる。
function AutoToggle({
  on,
  disabled,
  onToggle,
}: {
  on: boolean;
  disabled: boolean;
  onToggle: () => void;
}): ReactElement {
  return (
    <button
      type="button"
      role="switch"
      className="ferry-panel__auto-toggle"
      aria-checked={on}
      aria-disabled={disabled ? "true" : undefined}
      onClick={() => {
        if (!disabled) onToggle();
      }}
    >
      <span className="ferry-panel__auto-toggle-label">
        <span aria-hidden="true">🧭</span> 自動航行（ボット）
      </span>
      <span className="ferry-panel__auto-toggle-state" aria-hidden="true">
        {on ? "オン" : "オフ"}
      </span>
    </button>
  );
}
