# 共通のコマンドの入口（T-001 / ADR-0014）。
#
# まだない部分（server/go.mod・web/package.json など）は「対象なし」と出して飛ばす。
# test と lint は、実際に実行したものが失敗したときだけ失敗する。
#
# GNU make 3.81（macOS の標準）でも動くように、.ONESHELL・.SHELLFLAGS・
# .RECIPEPREFIX などの 3.82 以降の機能は使わない。レシピの bash も macOS の
# bash 3.2 で動く書き方にする（mapfile・空の配列の "${a[@]}" と set -u の組み合わせを避ける）。
# ルートで実行する前提（サブディレクトリからは実行しない）。

SHELL := bash
.DEFAULT_GOAL := help

.PHONY: help test lint gen gen-check sync-data dev dev-env clean-docker demo-local

# golangci-lint の版。.github/workflow-sources/ci.yml の server ジョブの
# GOLANGCI_LINT_VERSION と同じ版にする。PATH の golangci-lint は使わず、
# `go run` で server/go.mod の Go でビルドして実行する（T-005 / #13）。
GOLANGCI_LINT_VERSION := v2.15.0

# tygo の版。`go run` で server/go.mod の Go でビルドして実行する。server/go.mod には入れない。
# 上げるときは make gen をやり直して生成物も同じ PR でコミットする（T-301 / ADR-0014）。
TYGO_VERSION := v0.2.21

# レシピの先頭で読み込む小さな関数。
#   run  <cmd...>  実行して、失敗したら status=1 にする
#   skip <理由>    「（対象なし: 理由）」と出す
#   has  <cmd>     コマンドがあるか
PRELUDE = status=0; \
  run() { echo "==> $$*"; if ! "$$@"; then echo "失敗: $$*" >&2; status=1; fi; }; \
  skip() { echo "（対象なし: $$*）"; }; \
  has() { command -v "$$1" >/dev/null 2>&1; }; \
  shopt -s nullglob

help: ## 各ターゲットの説明を出す（既定）
	@echo "使い方: make <ターゲット>"
	@awk 'BEGIN { FS = ":.*## " } /^[a-z][a-z-]*:.*## / { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

test: ## Go（tools/*・server）・web・scripts/ の Python・tests/run.sh のテストを実行する
	@$(PRELUDE); \
	mods=(tools/*/go.mod); \
	if ! has go; then \
	  skip "go がない"; \
	else \
	  if [ $${#mods[@]} -eq 0 ]; then skip "tools/*/go.mod がない"; fi; \
	  for m in $${mods[@]+"$${mods[@]}"}; do run go -C "$${m%/go.mod}" test ./...; done; \
	  if [ -f server/go.mod ]; then run go -C server test ./...; \
	  else skip "server/go.mod がない。T-002 で作る"; fi; \
	fi; \
	if [ ! -f web/package.json ]; then skip "web/package.json がない。T-004 で作る"; \
	elif ! has pnpm; then skip "pnpm がない"; \
	else run pnpm -C web test; fi; \
	if ! has python3; then skip "python3 がない"; \
	else run python3 -m unittest discover -s scripts/tests -p 'test_*.py'; fi; \
	tests=(); \
	while IFS= read -r -d '' t; do tests+=("$$t"); done < <(git ls-files -z -- '*tests/run.sh'); \
	if [ $${#tests[@]} -eq 0 ]; then skip "tests/run.sh がない"; fi; \
	for t in $${tests[@]+"$${tests[@]}"}; do run bash "$$t"; done; \
	if [ $$status -ne 0 ]; then echo "make test: 失敗したものがある" >&2; fi; \
	exit $$status

lint: ## go vet・gofmt（tools/*・server）・golangci-lint（server）・web の lint と typecheck・shellcheck を実行する
	@$(PRELUDE); \
	gofmt_check() { \
	  local out; \
	  out="$$("$$(go -C "$$1" env GOROOT)/bin/gofmt" -l "$$1")" || return 1; \
	  if [ -n "$$out" ]; then echo "gofmt の差分がある:"; echo "$$out"; return 1; fi; \
	}; \
	dirs=(); \
	for m in tools/*/go.mod; do dirs+=("$${m%/go.mod}"); done; \
	if [ -f server/go.mod ]; then dirs+=(server); else skip "server/go.mod がない。T-002 で作る"; fi; \
	if ! has go; then \
	  skip "go がない"; \
	elif [ $${#dirs[@]} -eq 0 ]; then \
	  skip "Go のモジュールがない"; \
	else \
	  for d in "$${dirs[@]}"; do run go -C "$$d" vet ./...; run gofmt_check "$$d"; done; \
	  if [ -f server/go.mod ]; then \
	    run go -C server run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)" run ./...; \
	  fi; \
	fi; \
	if [ ! -f web/package.json ]; then skip "web/package.json がない。T-004 で作る"; \
	elif ! has pnpm; then skip "pnpm がない"; \
	else run pnpm -C web lint; run pnpm -C web typecheck; fi; \
	files=(); \
	while IFS= read -r -d '' f; do files+=("$$f"); done < <(git ls-files -z -- '*.sh'); \
	if ! has shellcheck; then skip "shellcheck がない"; \
	elif [ $${#files[@]} -eq 0 ]; then skip "*.sh がない"; \
	else run shellcheck -- "$${files[@]}"; fi; \
	if [ $$status -ne 0 ]; then echo "make lint: 失敗したものがある" >&2; fi; \
	exit $$status

gen: ## Go のプロトコルの型から TS の型を生成する（T-301）
	@if ! command -v go >/dev/null 2>&1; then echo "make gen: go がない" >&2; exit 1; fi; \
	echo "==> go -C server run github.com/gzuidhof/tygo@$(TYGO_VERSION) generate --config tygo.yaml"; \
	if ! go -C server run "github.com/gzuidhof/tygo@$(TYGO_VERSION)" generate --config tygo.yaml; then \
	  echo "make gen: tygo が失敗した" >&2; exit 1; \
	fi

gen-check: ## make gen をやり直して、web/src/protocol/ の生成物に差分がないかを確かめる（CI 用）
	@$(MAKE) --no-print-directory gen || exit 1; \
	untracked="$$(git ls-files --others --exclude-standard -- web/src/protocol/)"; \
	if ! git diff --exit-code -- web/src/protocol/ || [ -n "$$untracked" ]; then \
	  if [ -n "$$untracked" ]; then echo "未追跡のファイル:"; echo "$$untracked"; fi; \
	  echo "make gen-check: make gen の結果がコミットされていない（make gen を実行して web/src/protocol/ をコミットする）" >&2; \
	  exit 1; \
	fi; \
	echo "make gen-check: 差分なし"

# sync-data のコピー元とコピー先（テストで一時ディレクトリを渡すため変数にする）。
# コピー先の末尾は /embedded でなければならない（rm -rf の誤爆を防ぐ）。
SYNC_DATA_SRC ?= data
SYNC_DATA_DEST ?= server/internal/stage/embedded

sync-data: ## data/ の islands.json と stages/ を server/internal/stage/embedded/ にコピーする（-tags embeddata のビルド用。T-302）
	@src="$(SYNC_DATA_SRC)"; dest="$(SYNC_DATA_DEST)"; \
	case "$$dest" in \
	  */embedded) ;; \
	  *) echo "sync-data: SYNC_DATA_DEST の末尾は /embedded にする（値: $$dest）" >&2; exit 1 ;; \
	esac; \
	if [ ! -f "$$src/islands.json" ]; then echo "sync-data: $$src/islands.json がない" >&2; exit 1; fi; \
	rm -rf "$$dest" || exit 1; \
	mkdir -p "$$dest/stages" || exit 1; \
	cp "$$src/islands.json" "$$dest/islands.json" || exit 1; \
	n=0; \
	for f in "$$src"/stages/* "$$src"/stages/.[!.]*; do \
	  [ -f "$$f" ] || continue; \
	  cp "$$f" "$$dest/stages/" || exit 1; \
	  case "$$f" in *.yaml) n=$$((n + 1)) ;; esac; \
	done; \
	: >"$$dest/stages/.gitkeep" || exit 1; \
	echo "sync-data: $$src → $$dest（ステージ $$n 個）"

# dev・dev-env・clean-docker の中身は scripts/dev-env.sh（ポートの計算の正本。ADR-0019）。
dev: ## Go（standalone モード）と Vite を DEV_SLOT のポートで起動する（DRY_RUN=1 で表示だけ）
	@bash scripts/dev-env.sh dev $(if $(DRY_RUN),--dry-run)

dev-env: ## この作業ツリーの DEV_SLOT・ポート・cf の状態・Docker の名前を出す
	@bash scripts/dev-env.sh env

clean-docker: ## この作業ツリーの Docker のコンテナとイメージを消す（DRY_RUN=1 で表示だけ）
	@bash scripts/dev-env.sh clean-docker $(if $(DRY_RUN),--dry-run)

demo-local: ## デモの保険: ノート PC で Go（standalone・デモ操作あり）と Vite を起動する（CHECK=1 で準備の確認、DRY_RUN=1 で表示だけ）（T-504）
	@bash scripts/demo-local.sh $(if $(CHECK),--check) $(if $(DRY_RUN),--dry-run) $(if $(DEMO_DATA_DIR),--data "$(DEMO_DATA_DIR)")

.PHONY: check-secrets check-license

check-secrets: ## gitleaks で git の履歴にシークレットがないかを検査する（CI の secrets ジョブ。T-009）
	@bash scripts/check-secrets.sh

check-license: ## LICENSE・NOTICE が同梱の OSS・提供データの出典と合っているかを確かめる（CI の docs ジョブ。T-009）
	@python3 scripts/check-license.py
