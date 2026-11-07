// エンジニアモードを開いているか（T-712・FR-ENG-04）。開閉はエンジニアパネル（React）の中の状態なので、
// マップ（PixiJS、React の外）が毎フレーム getState() で読めるよう、小さな外部ストアに写す。
// ゲームの世界の状態ではないので gameStore には入れず、index.ts からも export しない（直接 import する）。
// ブラウザの保存領域には書かない（再読み込みで閉じた状態に戻る）。
import { useStore } from "zustand";
import { createStore } from "zustand/vanilla";
import type { StoreApi } from "zustand";

export type EngineerModeState = Readonly<{ open: boolean; setOpen(open: boolean): void }>;

export const engineerModeStore: StoreApi<EngineerModeState> = createStore<EngineerModeState>(
  (set, get) => ({
    open: false,
    setOpen(open) {
      if (get().open === open) return;
      set({ open });
    },
  }),
);

// React 用。開いているかだけを返す。
export function useEngineerMode(): boolean {
  return useStore(engineerModeStore, (s) => s.open);
}
