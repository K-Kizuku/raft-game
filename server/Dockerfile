# Go のゲームサーバー（server/cmd/server）のコンテナのイメージ（T-306）。
# cf build が web/cloudflare.config.ts の images.server からこのファイルでビルドし、
# Durable Object GameSession が MODE=single で起動する。Cloudflare Containers は linux/amd64 だけを動かす。
#
# 前提: data/ はビルドの文脈（server/）の外にあるので、先にリポジトリのルートで make sync-data を実行し、
#       server/internal/stage/embedded/ を作ってから、文脈を server/ にしてビルドする。
#   make sync-data
#   docker build --platform linux/amd64 -f server/Dockerfile --build-arg BUILD_ID=git:3f2c1ab server
# 手で試すとき: DEV_SLOT=<n> bash scripts/docker-smoke.sh
#
# Go の版は server/third_party/etcd-raft/go.mod の toolchain（go1.26.4）に合わせる（spike/container/Dockerfile と同じ）。

FROM --platform=linux/amd64 golang:1.26.4 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local GOFLAGS=-mod=readonly

# 依存の取得を層に分ける。go.mod の replace の先（third_party/etcd-raft）の go.mod がないと
# go mod download が失敗するので、両方を先に入れる。
COPY go.mod go.sum ./
COPY third_party/etcd-raft/go.mod third_party/etcd-raft/go.sum ./third_party/etcd-raft/
RUN go mod download

COPY . .

# go:embed の分かりにくい誤りより先に、make sync-data の忘れを知らせる。
RUN test -f internal/stage/embedded/islands.json || { echo "ステージ定義がない: リポジトリのルートで make sync-data を実行してからビルドする（server/internal/stage/embedded/）" >&2; exit 1; }

# ビルドの識別子（/healthz の build）。空なら BuildID が dev を返す。
# -ldflags の引数が壊れないよう、英数字と . _ : - の 64 文字までに限る。
ARG BUILD_ID=""
RUN export LC_ALL=C; \
    ok=1; \
    if [ "${#BUILD_ID}" -gt 64 ]; then ok=0; fi; \
    case "$BUILD_ID" in *[!A-Za-z0-9._:-]*) ok=0 ;; esac; \
    if [ "$ok" -ne 1 ]; then printf '%s\n' "BUILD_ID が不正: $BUILD_ID（英数字と . _ : - の 64 文字まで）" >&2; exit 1; fi

RUN go build -trimpath -buildvcs=false -tags embeddata -ldflags "-s -w -X main.build=${BUILD_ID}" -o /out/server ./cmd/server

# static: glibc なし（CGO_ENABLED=0 の静的なバイナリだけを置く）。CA 証明書・/etc/passwd（nonroot = 65532）・
# タイムゾーンのデータを含む。タグ nonroot は動くので、マニフェストリストのダイジェストで固定する
# （2026-11-07 に docker buildx imagetools inspect で linux/amd64 を含むことを確かめた）。
FROM --platform=linux/amd64 gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/server /server
USER 65532:65532
# GameSession は同じ値を env で渡す。手で docker run したときも single で動くように既定にする
# （standalone で動かすときは -e MODE=standalone で上書きする）。
ENV MODE=single PORT=8080
EXPOSE 8080
# exec の形。シェルを挟まないので SIGTERM が Go に直接届く。
# ヘルスチェックの命令は書かない（distroless にはシェルも HTTP のクライアントもない。
# Cloudflare Containers は使わず、GameSession が /healthz を呼んで確かめる）。
ENTRYPOINT ["/server"]
