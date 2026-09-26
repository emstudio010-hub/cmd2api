# cmd2api

把 Command Code 账号池转成 OpenAI / Anthropic 兼容接口的**自建网关**，带一个管理员后台。

从 [sub2api](https://github.com/Wei-Shaw/sub2api) 的数据模型与
[commandcode-proxy](https://github.com/MAXeaglet/commandcode-proxy) 的上游协议重新实现而来，
只保留账号池、密钥分发、用量统计、健康检查四件事。

> ⚠️ 使用前请阅读 [NOTICE](NOTICE) 与文末的免责声明。使用本项目可能违反上游服务商的条款。

---

## 它做什么

```
  Claude Code / OpenAI SDK / 任意兼容客户端
                 │
                 │  sk-c2a-xxxxxxxx   （cmd2api 签发的密钥）
                 ▼
        ┌─────────────────────────┐
        │        cmd2api          │
        │  密钥校验 → 分组 → 选账号 │
        │  协议转换 → 用量记账      │
        └─────────────────────────┘
                 │
                 │  user_xxxxxxxx     （Command Code 账号密钥）
                 ▼
         Command Code 上游 API
```

**管理员后台**管理三样东西：账号池（Command Code 账号）、分组（账号池与密钥之间的分配层）、
API Key（发给客户端用的密钥）。用量和账号健康在仪表盘和日志页看。

---

## 特性

- **两种协议入口** — `/v1/messages`（Anthropic，给 Claude Code）与 `/v1/chat/completions`（OpenAI）
- **账号池与故障转移** — 按优先级和负载均衡选账号；失败自动换下一个
- **并发闸门** — 每个账号独立限流，防止单个账号被压垮
- **健康检查与自动禁用** — 周期性探活，连续失败自动停用，恢复后自动启用
- **用量统计** — 按请求记录 token、耗时、首字延迟、状态码；仪表盘按时间/模型/账号聚合
- **额度控制** — 可给每把密钥设额度上限、有效期、IP 白名单
- **设备身份隔离** — 每个账号派生独立的设备指纹，会话保持稳定
- **仅管理员模式** — 没有注册、没有支付、没有多租户门户

---

## 快速开始

需要一个 Command Code 账号（密钥以 `user_` 开头）。

```bash
git clone <仓库地址> cmd2api && cd cmd2api
cp .env.example .env

# 生成密钥填进 .env
openssl rand -hex 24   # → JWT_SECRET
openssl rand -hex 32   # → ENCRYPTION_KEY
openssl rand -hex 24   # → DB_PASSWORD

docker compose up -d --build
```

打开 `http://127.0.0.1:8080`，用 `.env` 里的管理员邮箱和密码登录，然后：

1. **分组** → 新建一个分组
2. **账号** → 添加 Command Code 账号，绑定到该分组，点「测试连接」确认可用
3. **API Key** → 签发一把密钥

接进客户端：

```bash
# Claude Code
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=sk-c2a-你签发的密钥
claude
```

```bash
# OpenAI 兼容
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-c2a-你的密钥" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'
```

生产部署（Ubuntu + nginx + HTTPS）见 **[DEPLOY.md](DEPLOY.md)**。

---

## 本地开发

需要 Go 1.24+、Node 20+、pnpm、一个 Postgres。

```bash
# 后端
cd backend
export DB_HOST=127.0.0.1 DB_USER=cmd2api DB_PASSWORD=... DB_NAME=cmd2api
export JWT_SECRET=$(openssl rand -hex 24)
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export BOOTSTRAP_ADMIN_PASSWORD=devpassword
go run ./cmd/server          # 监听 :8080
```

```bash
# 前端（Vite 会把 /api 和 /v1 代理到 :8080）
cd frontend
pnpm install
pnpm dev                     # 监听 :5173
```

改了 `backend/ent/schema/` 下的模型后要重新生成 ORM 代码：

```bash
cd backend && go generate ./ent
```

跑测试：

```bash
cd backend && go test ./...
```

---

## 项目结构

```
cmd2api/
├── backend/
│   ├── ent/schema/            数据模型（ent ORM，启动时自动迁移）
│   ├── internal/
│   │   ├── relay/             ★ 上游协议：指纹、信封、NDJSON → SSE 翻译
│   │   ├── scheduler/         账号选择与并发闸门
│   │   ├── service/           账号服务、用量记账、启动引导
│   │   ├── handler/           HTTP 接口
│   │   ├── middleware/        管理员鉴权、API Key 鉴权
│   │   ├── crypto/            账号凭证的落库加密
│   │   └── server/            路由与静态资源
│   └── cmd/server/            入口
├── frontend/                  Vue 3 + Vite + Tailwind 管理后台
├── Dockerfile                 三段构建：前端 → 后端 → 精简运行时
├── docker-compose.yml         app + postgres
└── DEPLOY.md                  Ubuntu 部署与运维
```

---

## 配置

全部通过环境变量。完整清单和说明见 [.env.example](.env.example)，
后台「设置」页也能看到当前生效的值。

几个值得注意的：

| 变量 | 说明 |
|------|------|
| `ENCRYPTION_KEY` | 账号凭证的加密密钥。**丢了就解不出已录入的账号**，务必和数据库备份放一起 |
| `CC_FINGERPRINT_SALT` | 设备指纹的盐。改它等于让所有账号一起换设备，只在需要整体重置身份时动 |
| `CC_EMPTY_SYSTEM_PLACEHOLDER` | 默认 `true`。不带 system 时发空格占位，避免上游注入自带的 ~7.5K token 默认提示词 |
| `HEALTH_FAILURE_THRESHOLD` | 连续探活失败多少次后自动禁用账号 |

---

## 设计说明

几个不那么显然的决定，写在这里省得以后自己也想不起来：

**为什么并发闸门放在进程内存里，不用 Redis。**
按单实例部署设计的，进程内的 channel 就够了，少一个必须一起运维的组件。
代价是没法横向扩容——真要扩多实例时把 `internal/scheduler/limiter.go` 换成 Redis
信号量即可，接口不用动。

**为什么表结构用 ent 自动迁移，不用 SQL 迁移文件。**
表结构简单、没有历史包袱，自动迁移省掉了一整套「迁移文件编号冲突」的维护成本。
代价是没法做数据回填，真需要时再引入版本化迁移。

**为什么软删除不用 ent 的 Interceptor。**
sub2api 用 Interceptor 隐式改写所有查询加 `deleted_at IS NULL`。这里改成由
repository 显式带上条件：隐式改写排障时很难看出某条 SQL 到底带了什么条件。

**为什么金额是估算的。**
上游是订阅制，真实成本与 token 数不成正比。价格表（`internal/service/recorder.go`）
只是让用量统计有可比的量纲，**不对应任何真实扣款**。没有价格表条目的模型记 0，
而不是编一个看起来精确实则虚构的数字。

**为什么 threadId 只接受合法 UUID。**
上游会校验格式，非法值必须整个键省略——这是 CLI 的行为，照做。

**为什么 `x-project-slug` 不是配置里那个 `projectSlug`。**
真机上 slug = `slugify(workingDir)`，上游期望两者同源。原项目配置里的
`projectSlug` 实际未被使用，这里直接由项目目录算出来。

---

## 免责声明

- 本项目仅用于技术学习与研究。
- 使用本项目可能违反 Command Code、Anthropic、OpenAI 或其他上游服务商的**服务条款**。
  请在使用前自行阅读相关协议，风险自负。
- 作者不对账号封禁、服务中断、数据丢失或任何其他直接/间接损失负责。
- 请遵守你所在国家或地区的法律法规。

## 许可证

GNU LGPL-3.0（见 [LICENSE](LICENSE)）。来源与移植范围见 [NOTICE](NOTICE)。
