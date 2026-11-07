// 開会式の演出（T-415）の入口。ほかのモジュール（T-409 のチュートリアルなど）はここから import する。
export { OpeningCeremony } from "./OpeningCeremony.tsx";
export {
  isCeremonyActive,
  openingCeremonyState,
  useOpeningCeremonyActive,
} from "./ceremonyState.ts";
export type { CeremonySnapshot, OpeningCeremonyStore } from "./ceremonyState.ts";
export { OPENING_CEREMONY } from "./config.ts";
export { islandLabel, openingResult } from "./openingView.ts";
export type { OpeningResult } from "./openingView.ts";
