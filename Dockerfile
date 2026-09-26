# cmd2api 一体化镜像：前端构建 → 后端构建 → 精简运行时。
#
# 三段构建的意义：最终镜像里既没有 Node、也没有 Go 工具链和源码，
# 只有一个静态二进制加一份前端产物。这既压小体积，也减少攻击面。

# ---------- 1. 前端构建 ----------
FROM node:24-alpine AS frontend

WORKDIR /src/frontend

# 先只拷贝依赖清单再装依赖：源码改动不会让这层缓存失效，
# 重复构建时能省掉几轮 npm 解析。
COPY frontend/package.json frontend/pnpm-lock.yaml* ./
RUN corepack enable && pnpm install --frozen-lockfile || pnpm install

COPY frontend/ ./
RUN pnpm build


# ---------- 2. 后端构建 ----------
FROM golang:1.24-alpine AS backend

WORKDIR /src/backend

RUN apk add --no-cache git

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# CGO_ENABLED=0 产出静态链接二进制，才能直接跑在 alpine 上。
# 去掉调试符号和符号表能让二进制小一截。
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/cmd2api \
      ./cmd/server


# ---------- 3. 运行时 ----------
FROM alpine:3.20

# 刻意不装任何包，这一层不需要联网：
#   · CA 证书    —— alpine 基础镜像自带，访问 https 上游靠它
#   · 时区数据库 —— 已用 _ "time/tzdata" 内嵌进二进制
#   · wget       —— busybox 自带，健康检查够用
# 一个不联网的运行时阶段意味着镜像构建不会卡在包源上，体积也更小。
RUN adduser -D -H -u 10001 cmd2api

WORKDIR /app

COPY --from=backend /out/cmd2api /app/cmd2api
COPY --from=frontend /src/frontend/dist /app/web

# 以非 root 运行：容器被攻破时能做的事情少很多。
USER cmd2api

ENV STATIC_DIR=/app/web \
    SERVER_HOST=0.0.0.0 \
    SERVER_PORT=8080 \
    GIN_MODE=release \
    TZ=Asia/Shanghai

EXPOSE 8080

# 探针直接打 /health，它不依赖数据库，能区分「进程活着」和「依赖正常」。
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/cmd2api"]
