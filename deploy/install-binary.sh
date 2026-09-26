#!/usr/bin/env bash
# cmd2api 直接部署（不使用 Docker）的安装脚本。
#
# 适用场景：宿主机已有 PostgreSQL，或者你想用系统自带的、想被 systemd 直接管起来。
# 用 Docker 的话不需要这个脚本，见 DEPLOY.md。
#
# 用法（在解压出来的目录里执行）：
#   sudo ./install-binary.sh
#
# 脚本是幂等的，重复执行只会更新二进制和前端产物，不会动 /etc/cmd2api/env。

set -euo pipefail

APP_DIR=/opt/cmd2api
CONF_DIR=/etc/cmd2api
CONF_FILE="$CONF_DIR/env"
SERVICE=cmd2api
UNIT_SRC="$(cd "$(dirname "$0")" && pwd)/systemd/cmd2api.service"

say()  { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33m警告:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m错误:\033[0m %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "需要 root 权限运行：sudo $0"
[[ -f ./cmd2api ]] || die "当前目录下找不到 cmd2api 二进制，请在解压出来的目录里运行"
[[ -d ./web ]]     || die "当前目录下找不到 web/（前端产物）"

# ---- 1. 专用系统用户 ----
# 不给登录 shell、不建 home：这个账号只用来跑进程。
if id "$SERVICE" &>/dev/null; then
  say "系统用户 $SERVICE 已存在，跳过"
else
  say "创建系统用户 $SERVICE"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE"
fi

# ---- 2. 安装程序与前端产物 ----
say "安装到 $APP_DIR"
install -d -o "$SERVICE" -g "$SERVICE" -m 0755 "$APP_DIR" "$APP_DIR/tmp"
install -o "$SERVICE" -g "$SERVICE" -m 0755 ./cmd2api "$APP_DIR/cmd2api"

# web/ 每次整体替换，避免旧版本的带指纹资源残留。
rm -rf "$APP_DIR/web"
cp -r ./web "$APP_DIR/web"
chown -R "$SERVICE:$SERVICE" "$APP_DIR/web"

# ---- 3. 配置 ----
install -d -m 0750 "$CONF_DIR"
if [[ -f "$CONF_FILE" ]]; then
  say "配置文件已存在，保留不动：$CONF_FILE"
else
  say "生成配置文件 $CONF_FILE（含随机密钥）"
  umask 077

  gen() { openssl rand -hex "$1"; }
  cat > "$CONF_FILE" <<EOF
# cmd2api 配置。权限应为 0640 root:$SERVICE —— 里面有数据库口令和加密密钥。
#
# 注意 ENCRYPTION_KEY：它加密已录入的上游账号凭证。
#   丢了 → 已录入的账号全部解不出来，只能重新录入
#   换了 → 同上
# 请和数据库备份一起保存。

SERVER_HOST=0.0.0.0
SERVER_PORT=8080
GIN_MODE=release
LOG_LEVEL=info
TZ=$(timedatectl show -p Timezone --value 2>/dev/null || echo Asia/Shanghai)
STATIC_DIR=$APP_DIR/web

# 数据库。默认按本机 PostgreSQL 填，远程库请自行改 host/port。
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=cmd2api
DB_NAME=cmd2api
DB_PASSWORD=$(gen 24)
DB_SSLMODE=disable

JWT_SECRET=$(gen 24)
JWT_TTL=24h
ENCRYPTION_KEY=$(gen 32)

# 初始管理员。只在系统里一个管理员都没有时用来创建账号，
# 之后改密码请走后台界面——改这里不会覆盖已存在的管理员。
BOOTSTRAP_ADMIN_EMAIL=admin@cmd2api.local
BOOTSTRAP_ADMIN_PASSWORD=$(gen 12)

# 浏览器授权时 studio 把浏览器跳回来的地址前缀。
# 留空即按请求的 Host 推出来，本地和隧道场景都对；挂了反代且推错时
# 在这里填对外域名，例如 https://cmd2api.example.com
PUBLIC_BASE_URL=

# ---- Command Code 上游 ----
CC_API_BASE=https://api.commandcode.ai
# 服务器在国内、访问上游需要走代理时填这里，例如 http://127.0.0.1:7890
CC_UPSTREAM_PROXY=
CC_EMPTY_SYSTEM_PLACEHOLDER=true
CC_STREAM_IDLE=30s
CC_NONSTREAM_IDLE=90s

# ---- 账号健康检查 ----
HEALTH_CHECK_ENABLED=true
HEALTH_CHECK_INTERVAL=10m
HEALTH_FAILURE_THRESHOLD=5
EOF

  chown root:"$SERVICE" "$CONF_FILE"
  chmod 0640 "$CONF_FILE"

  DB_PW=$(grep '^DB_PASSWORD=' "$CONF_FILE" | cut -d= -f2-)
  ADMIN_PW=$(grep '^BOOTSTRAP_ADMIN_PASSWORD=' "$CONF_FILE" | cut -d= -f2-)

  echo
  say "已生成随机密钥。请记下这两个值："
  echo "    数据库口令:   $DB_PW"
  echo "    管理员初始密码: $ADMIN_PW"
  echo
  warn "数据库口令是随机生成的，但数据库里还没有这个用户。"
  warn "请先创建数据库和用户，例如："
  echo "      sudo -u postgres psql -c \"CREATE USER cmd2api WITH PASSWORD '$DB_PW';\""
  echo "      sudo -u postgres psql -c \"CREATE DATABASE cmd2api OWNER cmd2api;\""
  echo
fi

# ---- 4. systemd ----
say "安装 systemd 单元"
[[ -f "$UNIT_SRC" ]] || die "找不到 $UNIT_SRC"
install -m 0644 "$UNIT_SRC" "/etc/systemd/system/$SERVICE.service"
systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null
systemctl restart "$SERVICE"

# ---- 5. 自检 ----
say "等待服务就绪"
ok=0
for _ in $(seq 1 30); do
  if curl -fsS -m 2 http://127.0.0.1:8080/health &>/dev/null; then ok=1; break; fi
  sleep 1
done

if [[ $ok -eq 1 ]]; then
  say "服务已就绪：http://127.0.0.1:8080"
  echo
  echo "  查看日志:   journalctl -u $SERVICE -f"
  echo "  改配置:     编辑 $CONF_FILE 后 systemctl restart $SERVICE"
  echo "  数据库还没建的话，见上面那段提示。"
else
  warn "服务起来了但健康检查没通过，先看日志排查："
  echo
  journalctl -u "$SERVICE" -n 30 --no-pager
  exit 1
fi
