# スパイク（T-007a / #134）のコンテナ。Cloudflare Containers は linux/amd64 だけを動かす。
# Go の版は server/third_party/etcd-raft の toolchain（go1.26.4）に合わせて固定する。
FROM --platform=linux/amd64 golang:1.26.4 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/spike-server .

FROM --platform=linux/amd64 scratch
# M-9 の対照（enableInternet: true）で HTTPS を使うための CA 証明書
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/spike-server /spike-server
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/spike-server"]
