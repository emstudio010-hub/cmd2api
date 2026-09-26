# 部署到 Ubuntu

面向 Ubuntu 22.04 / 24.04 的部署步骤。

有两条路，按你的情况选一条：

| 方式 | 适合 | 需要什么 |
|------|------|----------|
| **[A. Docker](#a-docker-部署)** | 想省事、环境干净、将来好升级 | 只要 Docker |
| **[B. 直接跑二进制](#b-直接跑二进制systemd)** | 宿主机已有 PostgreSQL，或不想装 Docker | 一个 Linux 二进制 + systemd |

两条路用的是同一份配置项，区别只在谁来管进程。

---

# A. Docker 部署

全程只需要 Docker，不需要在宿主机装 Go、Node 或 Postgres。

---

## 1. 安装 Docker

```bash
# 官方脚本装 Docker Engine + compose 插件
curl -fsSL https://get.docker.com | sudo sh

# 让当前用户能免 sudo 用 docker（重新登录后生效）
sudo usermod -aG docker $USER
newgrp docker

# 验证
docker --version && docker compose version
```

---

## 2. 获取代码

```bash
git clone <你的仓库地址> cmd2api
cd cmd2api
```

---

## 3. 生成配置

```bash
cp .env.example .env
```

生成三个密钥并填进 `.env`：

```bash
echo "DB_PASSWORD=$(openssl rand -hex 24)"
echo "JWT_SECRET=$(openssl rand -hex 24)"
echo "ENCRYPTION_KEY=$(openssl rand -hex 32)"
```

> **`ENCRYPTION_KEY` 必须备份。** 它用来加密已录入的 Command Code 账号密钥。
> 丢了或换了，所有账号都得重新录入。请把它和数据库备份放在一起。

再设置管理员初始密码：

```bash
# 编辑 .env
BOOTSTRAP_ADMIN_EMAIL=you@example.com
BOOTSTRAP_ADMIN_PASSWORD=<一个至少 8 位的强密码>
```

---

## 4. 启动

```bash
docker compose up -d --build
```

首次构建要编译前端和后端，大约 2–5 分钟（取决于机器）。之后启动很快。

查看状态与日志：

```bash
docker compose ps
docker compose logs -f app
```

启动正常的话会看到 `数据库已就绪`、`HTTP 服务已监听`。

---

## 5. 首次登录

默认只监听回环地址，先在服务器上验证：

```bash
curl -s http://127.0.0.1:8080/health
# {"status":"ok"}
```

要在本机浏览器访问，用 SSH 端口转发：

```bash
ssh -L 8080:127.0.0.1:8080 user@你的服务器
# 然后浏览器打开 http://127.0.0.1:8080
```

用 `.env` 里设的邮箱和管理员密码登录。

> 登录后第一件事：**改掉初始密码**（右上角账号菜单 → 修改密码）。
> 环境变量里的 `BOOTSTRAP_ADMIN_PASSWORD` 只在系统里没有管理员时生效，
> 之后不会再覆盖你改过的密码。

---

## 6. 配置 nginx 反向代理 + HTTPS

中转是流式长连接，nginx 有两个地方必须调，否则流会被掐断：

```nginx
server {
    listen 443 ssl http2;
    server_name api.example.com;

    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # ★ 必须关掉缓冲，否则 SSE 流会被 nginx 攒着一起发，
        #   客户端要等整段回答结束才看到第一个字。
        proxy_buffering off;
        proxy_cache off;

        # ★ 读超时必须放大。长回答（尤其带工具调用）很容易超过默认的 60s，
        #   超时会让 nginx 返回 504，而请求其实还在正常进行。
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}

server {
    listen 80;
    server_name api.example.com;
    return 301 https://$host$request_uri;
}
```

证书用 certbot：

```bash
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d api.example.com
```

配好之后把 `.env` 里的 `BIND_ADDR` 保持 `127.0.0.1`（只让 nginx 能访问）。

---

## 7. 接入客户端

先在后台 **分组** 里建一个分组，在 **账号** 里添加 Command Code 账号（密钥以 `user_` 开头）并绑定到该分组，然后在 **API Key** 里签发一把密钥。

### Claude Code

```bash
export ANTHROPIC_BASE_URL=https://api.example.com
export ANTHROPIC_AUTH_TOKEN=sk-c2a-你签发的密钥
claude
```

### OpenAI 兼容客户端

```
Base URL: https://api.example.com/v1
API Key:  sk-c2a-你签发的密钥
```

### 验证

```bash
curl https://api.example.com/v1/chat/completions \
  -H "Authorization: Bearer sk-c2a-你的密钥" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'
```

---

## 8. 日常运维

### 升级

```bash
git pull
docker compose up -d --build
```

数据库表结构由后端启动时自动迁移，不需要手工执行 SQL。

### 备份

数据库是唯一需要备份的东西：

```bash
docker compose exec -T postgres pg_dump -U cmd2api cmd2api \
  | gzip > cmd2api-$(date +%F).sql.gz
```

恢复：

```bash
gunzip -c cmd2api-2026-01-01.sql.gz \
  | docker compose exec -T postgres psql -U cmd2api cmd2api
```

> 别忘了 `ENCRYPTION_KEY`——没有它，备份里的账号密钥解不出来。

### 看日志

```bash
docker compose logs -f app          # 实时
docker compose logs --tail=200 app  # 最近 200 行
```

日志是 JSON，可以用 `jq` 过滤：

```bash
docker compose logs app | jq -r 'select(.level=="ERROR") | .msg'
```

### 进数据库排查

```bash
docker compose exec postgres psql -U cmd2api
```

### 停止 / 清理

```bash
docker compose down          # 停止，保留数据
docker compose down -v       # 停止并删除数据卷（⚠️ 数据全没）
```

---

## 9. 常见问题

**启动时报 `数据库在 N 次重试后仍不可用`**
Postgres 容器还没就绪，或者 `DB_PASSWORD` 与 Postgres 初始化时用的不一致。注意：Postgres 只在**首次**初始化数据卷时读 `POSTGRES_PASSWORD`，之后改 `.env` 不会改数据库里的口令。改过口令的话要么把 `.env` 改回去，要么删掉数据卷重来。

**启动时报 `ENCRYPTION_KEY 必须解出 32 字节`**
`ENCRYPTION_KEY` 得是 64 位十六进制（`openssl rand -hex 32`），32 个字符是不够的。

**账号测试连接失败，提示密钥无法解密**
录入之后换过 `ENCRYPTION_KEY`。把旧密钥换回去，或者删除账号重新录入。

**客户端能连上但回答很慢、要等很久才出第一个字**
nginx 的 `proxy_buffering` 没关。见第 6 节。

**容器一直显示 unhealthy，但 `curl http://127.0.0.1:8080/health` 是正常的**
说明健康检查命令被代理环境变量带偏了。Docker Desktop（Windows/macOS）会给每个容器注入
`HTTP_PROXY=http://127.0.0.1:7890` 之类的变量，而那个代理跑在宿主机上、容器里并不存在，
busybox 的 `wget` 又只认这个变量、不看 `no_proxy`，于是连「探测容器自己」都会被送去
那个不存在的代理。

本项目的 Dockerfile 里已经在探针命令前清空了这些变量，所以正常不会遇到。如果你自己改了
探针，记得保留 `http_proxy= https_proxy= HTTP_PROXY= HTTPS_PROXY=` 这个前缀。

**容器连不上上游（超时 / connection refused）**
先确认是不是被同样的代理变量误导了：容器里的 `127.0.0.1` 指的是容器自己，不是宿主机。
如果你的网络确实需要走代理才能访问上游，把代理地址填到 `CC_UPSTREAM_PROXY`：

```bash
# .env
# 指向宿主机上的代理（compose 已经配好 host.docker.internal 的映射）
CC_UPSTREAM_PROXY=http://host.docker.internal:7890
```

只支持 `http://` / `https://` 代理。填错的话服务会在启动时直接报错并指出问题，不会带着
坏配置跑起来。

**`/v1/messages` 报 503「分组内没有可用账号」**
分组里没有账号，或者账号都被禁用了。去后台看一下账号状态和最近一次探活结果。

**账号被自动禁用了**
连续探活失败达到阈值（默认 5 次）。在账号列表能看到最后一次的错误原因。修好之后点「测试连接」，成功会自动恢复启用。

---

# B. 直接跑二进制（systemd）

不用 Docker，把编译好的二进制交给 systemd 管。适合宿主机已经跑着 PostgreSQL、
或者你希望少一层容器。

## B1. 准备

需要：Linux x86_64、systemd、一个可用的 PostgreSQL。二进制是**静态链接**的，
不依赖 glibc 之外的东西，也不需要在机器上装 Go 或 Node（前端产物已经打包在
`web/` 里）。

从 release 页面下载 `cmd2api-vX.Y.Z-linux-amd64.tar.gz` 并解压：

```bash
tar xzf cmd2api-vX.Y.Z-linux-amd64.tar.gz
cd cmd2api-vX.Y.Z-linux-amd64
```

## B2. 安装

```bash
sudo ./install-binary.sh
```

脚本会做这些事（可重复执行，不会覆盖已有配置）：

1. 建一个不可登录的系统用户 `cmd2api`
2. 把二进制和前端产物装到 `/opt/cmd2api`
3. 生成 `/etc/cmd2api/env`，并**随机生成** `DB_PASSWORD` / `JWT_SECRET` /
   `ENCRYPTION_KEY` / 管理员初始密码
4. 装上并启动 systemd 单元

它会打印出随机生成的数据库口令和管理员密码，**记下来**。

## B3. 建数据库

脚本生成的口令是随机的，但数据库里还没有对应的用户，需要建一次：

```bash
DB_PW='把脚本打印出来的口令粘这里'
sudo -u postgres psql -c "CREATE USER cmd2api WITH PASSWORD '$DB_PW';"
sudo -u postgres psql -c "CREATE DATABASE cmd2api OWNER cmd2api;"
sudo systemctl restart cmd2api
```

表结构由后端启动时自动迁移，不需要手工执行 SQL。

## B4. 确认

```bash
curl -s http://127.0.0.1:8080/health     # {"status":"ok"}
journalctl -u cmd2api -f                 # 看日志
```

后面的「首次登录」「nginx + HTTPS」「接入客户端」与 Docker 方式完全一样，
见第 5、6、7 节。

## B5. 日常运维

```bash
sudo systemctl restart cmd2api        # 改完配置重启
sudo journalctl -u cmd2api -n 100     # 看最近日志
sudo systemctl status cmd2api         # 状态
```

升级：解压新版本的包，再跑一次 `sudo ./install-binary.sh` 即可——
它只替换二进制和 `web/`，`/etc/cmd2api/env` 保持不动。

备份（和 Docker 方式一样，数据库是唯一要备份的东西）：

```bash
pg_dump -U cmd2api cmd2api | gzip > cmd2api-$(date +%F).sql.gz
```

> 同样别忘了 `/etc/cmd2api/env` 里的 `ENCRYPTION_KEY`。没有它，
> 备份里的账号凭证解不出来。

## B6. 配置项在哪

全部在 `/etc/cmd2api/env`（权限 `0640 root:cmd2api`，里面有凭证）。
每一项的说明见仓库里的 `.env.example`。改完执行
`sudo systemctl restart cmd2api`。

两个最容易踩的：

- `CC_UPSTREAM_PROXY` —— 服务器访问境外上游需要走代理时填，例如
  `http://127.0.0.1:7890`。留空表示直连。
- `ENCRYPTION_KEY` —— **不能丢也不能换**，见上面的说明。
