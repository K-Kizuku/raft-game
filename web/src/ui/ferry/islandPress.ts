// マップで島を押したことの知らせ（#318・ゲームデザイン §3.2）。マップ（web/src/game）は押した島を伝えるだけで、
// 行き先にするかはフェリー操作パネルが決める（船を選んでいて、港にいて、隣の島のときだけ）。
// 選択（gameStore の selection.islandId）は同じ島を押し直しても変わらないので、押したこと自体をここで知らせる。
export type IslandPressListener = (islandId: number) => void;

const listeners = new Set<IslandPressListener>();

// pressIsland はマップで島 islandId が押されたことを知らせる。
export function pressIsland(islandId: number): void {
  for (const listener of [...listeners]) listener(islandId);
}

// onIslandPress は島が押されたときに listener を呼ぶ。返り値の関数で外す。
export function onIslandPress(listener: IslandPressListener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
