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
- **账号余额** — 跟着探活一起拉上游额度，账号列表直接看「还剩多少钱」、套餐用量百分比、5 小时/周限流窗口；也可单独刷新（不消耗生成额度）
- **额度窗口参与调度** — 5 小时/周窗口已经打满的账号不再被优先选用，省掉一次注定被上游拒的往返；快照过时或全池都打满时照旧回退到原来的行为
- **浏览器授权导入账号** — 用 Command Code 账号在浏览器里登录一次就把账号加进来，不必手工去别处抠密钥；也可以直接贴 `~/.commandcode/auth.json` 的内容
- **授权自动拾取也能手动粘贴** — 浏览器授权在新标签页里完成，当前页面轮询到结果后自己刷新；跳转地址打不开时（面板在远程服务器上很常见）把地址栏内容粘回来一样能建成账号
- **改密码与改用户名** — 右上角账号菜单里可以直接换登录邮箱，两者都要验证当前密码
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

加账号有三种方式，效果完全一样——浏览器授权拿到的最终产物就是一把普通密钥：

- **用浏览器登录** — 点「用浏览器登录」，在 Command Code 页面登录并授权，浏览器会自动跳回来把账号建好
- **贴密钥** — 从别处拿到 `user_` 开头的密钥，直接粘贴
- **贴 auth.json** — 如果本机已经跑过官方 CLI，把 `~/.commandcode/auth.json` 的内容整个贴进「批量导入」，账号名会取自文件里的 `userName` 和 `keyName`；几个文件首尾相接一起贴也行

如果 cmd2api 不在你浏览器能直接访问到的地址上（比如后面挂了反代），浏览器授权那一步需要设 `PUBLIC_BASE_URL`，见[配置](#配置)。

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
cd backend && go test ./...      # 不需要数据库，需要数据库的那几条会自动跳过
```

`internal/scheduler` 里有几条**必须连真实 PostgreSQL** 的测试——它们针对的是
读-改-写丢更新，只有并发事务真的跑起来才暴露得出来，用假的客户端测不出来。
没设 `CMD2API_TEST_DATABASE_URL` 时它们会跳过并打印跑法。注意 postgres 在
compose 里是**故意不对外映射端口**的，所以从宿主机直连不上，得把测试跑在
compose 网络里：

```bash
docker compose exec -T postgres createdb -U cmd2api cmd2api_test   # 单独建库
docker build --target backend -t cmd2api-be-test .
docker run --rm --network cmd2api_cmd2api \
  -e CMD2API_TEST_DATABASE_URL="host=postgres port=5432 user=cmd2api password=$DB_PASSWORD dbname=cmd2api_test sslmode=disable" \
  cmd2api-be-test sh -c "cd /src/backend && go test ./internal/scheduler/ -v"
```

那几条测试每轮都会清空目标库的 `accounts` 表，所以务必用单独建的库。

单元测试之外还有一个端到端冒烟脚本，对着**真的跑起来的服务**发请求，覆盖登录、
分组、账号、API Key、中转入口这些串起来才看得出的路径：

```bash
set -a; . ./.env; set +a          # 从 .env 取管理员账号
CC_ADMIN_EMAIL="$BOOTSTRAP_ADMIN_EMAIL" \
CC_ADMIN_PASSWORD="$BOOTSTRAP_ADMIN_PASSWORD" \
python backend/scripts/e2e_smoke.py http://127.0.0.1:8080
```

它只登录、不建号，所以 `BOOTSTRAP_ADMIN_PASSWORD` 必须已经生效过。这里有个坑：
那个变量只在**首次启动、库里还没有管理员**时起作用，启动之后再去 `.env` 改它，
管理员密码不会跟着变——脚本检测到登录失败时会把这句话再提醒一遍。

它每次运行都用带时间戳的随机后缀命名测试资源，所以对着已经有数据的实例反复跑
也不会撞名、不会搅乱真实数据。

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
| `PUBLIC_BASE_URL` | 浏览器授权时 studio 把浏览器跳回来的地址前缀。留空则按请求的 `Host` 和 `X-Forwarded-Proto` 推出来——本地、SSH 隧道、反代域名三种情况都能自适应，只有推错时才需要显式写死 |

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

**为什么余额的套餐总额是写死的表。**
上游的 `/alpha/billing/credits` 只返回「还剩多少」，不返回「总额」——没有分母就
算不出百分比。总额只能从官方 CLI 包里内嵌的那张套餐表反查（见
`internal/relay/balance.go`）。这张表会随上游改价过期，所以**查不到的套餐一律
按未知处理**：界面上少一行百分比，好过拿一个猜出来的分母去算。剩下多少钱这个
主要数字不受影响，它直接来自上游。

这些端点都不在公开文档里，是从 `command-code@1.66.0` 的发行包中读出来的。

**为什么余额刷新失败不更新 `balance_fetched_at`。**
那个字段的含义是「界面上这个数字是什么时候取的」。失败时把它推到当前时间，
会让一个三天前的余额看起来像刚刚取的。保持原值，界面就能诚实地显示
「$8.69（3 天前）」外加一条刷新失败。

**为什么 threadId 只接受合法 UUID。**
上游会校验格式，非法值必须整个键省略——这是 CLI 的行为，照做。

**为什么 `x-project-slug` 不是配置里那个 `projectSlug`。**
真机上 slug = `slugify(workingDir)`，上游期望两者同源。原项目配置里的
`projectSlug` 实际未被使用，这里直接由项目目录算出来。

**为什么版本号是写死的，以及怎么升它。**
`internal/relay/client.go` 里的 `protocolVersion` 不跟着 npm 上跑。真机发出去的
永远是「形状 + 版本号自洽」的组合；版本号涨了形状没跟上，就变成「自称最新版、
却说旧方言」——这比版本号略旧更容易被行为分析挑出来。所以升级的正确顺序是：
先把新包读一遍对齐形状，再改这个常量，并把核对过的东西写在常量上面。
1.66.0 已经按这个流程对过一遍，形状没变，唯一改掉的是会话 ID 的形状。

**为什么会话 ID 必须长得像 CLI 生成的。**
CLI 的 `generateSessionId` 产出的是 `sess_` + UUID 去横线后的前 16 位十六进制。
我们原来直接发一个带横线的裸 UUID——这个值出现在**每一个**请求的 `x-session-id`
头上，形状不对是一条一眼就能看出来的差异，比版本号旧明显得多。
同理，生命周期事件里报的会话 ID 必须和请求头里那个是同一个：真机全程只有一个会话，
报一个、用另一个，等于凭空多出一个从没在别处出现过的会话。

**额度窗口为什么要参与调度，以及为什么只认「已经打满」。**
上游的 5 小时/周窗口是硬限制，打满之后请求会被直接拒。余额快照里本来就有这个信息，
那就没必要非等上游拒一次再换账号——省掉一次注定失败的往返。
但只跳过**已经打满**的账号，不设「快打满了」（比如 98%）这种预警阈值：
那不是上游给的信号，是我们自己编的，而编出来的阈值会让账号在其实还能用的时候被
绕开，那种偏差比偶尔被上游拒一次更难查。

另外两条约束。一是**只在快照新鲜时**（30 分钟，三个探活周期）才下这个判断：
余额链路自己坏掉时，拿旧快照去跳过账号会让账号一直不被使用，
而根因（拉不到余额）反而被这个副作用盖住。二是打满的账号**留作兜底**而不是滤掉：
全池都打满时把请求发出去，上游的拒绝是个准确的答复，比我们凭一份最多十分钟前的
快照直接说「没有可用账号」要好。所以这是「优先用能用的」，不是「宁可失败也不用」。

**浏览器授权为什么拿到一把普通密钥就结束了。**
Command Code 的授权流程（`/studio/auth/cli`，从 CLI 包里读出来的，不在公开文档里）
最终交给回调方的就是一把跟手工粘贴完全一样的 API key，没有额外的 refresh token、
没有 OAuth 授权码换 token 那一步。所以这条链路只改「密钥怎么进来」，中转、调度、
表结构一行都没动，两条导入路径随时可以互换。

**浏览器授权为什么必须有 state，而且只能用一次。**
发起授权的是后台接口，身份是 `Authorization` 头里的 JWT；但回调是 studio 让**浏览器**
跳过来的，顶层导航带不了自定义头，所以回调拿不到任何身份信息。整个握手里唯一
能证明「这次回调对应我发起的那一次」的就是那 32 字节随机 `state`：10 分钟过期、
取走即作废（`take` 是删读，不是查读）。它同时也是 CSRF 防线——没有它，任何人构造
一个带 `apiKey` 的回调地址就能往你账号池里塞账号。

**为什么回调路径在鉴权之外，以及密钥为什么不会漏进日志。**
回调必须跳过 AdminAuth（浏览器跳转没法带头），身份完全由 `state` 认。而 studio 是把
`apiKey` 放在**回调的 query 里**传回来的，所以这条路径上有一瞬间明文密钥在 URL 里。
处理办法是：把密钥取出来之后立刻用掉，回跳前端时只带 `?oauth=ok&id=..&name=..`，
**绝不把密钥写进任何一个 Redirect 的 Location**（`router_test.go` 里有一条断言盯着
这件事）。另外访问日志只记 `URL.Path`、不记 `RawQuery`，否则密钥会直接落进日志文件。

**为什么 auth.json 的解析在后端而不是前端。**
前端本来能顺手 `JSON.parse` 一下，但这份解析有真的边界情况：多个文件首尾相接
（不是合法 JSON）、字符串里带花括号、转义引号、一个坏条目不能连累整批。
前端只有 `vue-tsc` 和 `vite build`，没有测试框架；后端有。所以解析收在
`parseKeyLines` 一处，`account_keys_test.go` 盯着它，前端只负责认出「这是 auth.json」
并给一句提示。真写过一遍才知道值得：测试当场抓出一个参数顺序写反的 bug。

**为什么账号名用 `userName · keyName`。**
同一个人的多把密钥（cli、desktop…）在账号列表里长得一模一样，只按 `userName`
命名会分不清哪个是哪个。`keyName` 缺失时就只用 `userName`，都不缺才算完整名字。

**为什么改了 auth.json 之后 frontend 还得自己再解析一遍按行格式。**
它不用——按行格式（`名称,密钥`）确实有两份实现，因为操作员需要在**提交前**就看到
哪一行会被丢掉，而不是提交完再对着一串错误猜。这份重复只覆盖规则最简单的部分，
auth.json 那种带边界情况的解析没有第二份。两边不一致的风险落在「逗号切分」这一条
规则上，够简单，也够显眼。

**为什么兜底模型表是整张照搬，而不是手挑一份。**
`GET /provider/v1/models` 是主路径，拉不到时才用 `hardcodedModels` 兜底。这张表
上一版是手挑的 26 条，一个 CLI 版本就被甩下了（Opus 5.5、GPT-6 Luna/Sol、Grok 4.7
全没跟上）。所以现在不手挑：`backend/scripts/refresh_models.py` 直接从本地装的
`command-code` 包里把 CLI 自己那张模型目录抠出来，按上游顺序整张抄进 `client.go`，
只去掉标了 `hidden` 的条目。探包体有两个坑，脚本里都注掉了：`hidden` 除了字面量
还有 `get hidden(){...}` 这种 getter（促销结束自动隐藏），只认字面量会把免费模型
当成正式模型；另外锚点 id 落在**条目自己**的花括号里，直接配对切出来只有一条模型，
而脚本还会"成功"跑完不报错。这是数据不是协议形状，改它不影响 wire 兼容性。

**浏览器授权为什么有三条收尾路径，而不是一条。**
授权流程本身是官方 CLI 那套：浏览器跳到 commandcode.ai 登录，studio 再把浏览器
跳回**回调地址**，密钥挂在 query 上。问题出在"跳回来"这一步——回调地址必须指向
跑着 cmd2api 的那台机器，而面板常常部署在远程服务器上，浏览器根本连不过去。那时
页面会停在一个打不开的地址上，密钥就明明白白地在那个地址里。

所以：**自动**那条（轮询）是主路径，在新标签页里完成授权，发起授权的页面每两秒问
一次 `…/status`，拿到结果就自己收工——不再整页跳走，表单填了一半的东西也不会丢。
**手动**那条（`…/complete`）是给上述场景兜底的：用户把地址栏里那条打不开的地址整段
粘回来，后端从中取密钥建号；直接粘一把裸密钥也认。两条路建出来的账号完全一样，因为
它们共用 `createAccountFromKey`——分成两份实现迟早会分叉出"自动建的账号有余额、
手动建的没有"这种说不清的不一致。

手动这条路要挂 `AdminAuth`，回调那条**必须**不挂（浏览器顶层跳转带不了
`Authorization` 头）。这不是放松要求，是更严：能调手动那条的人必须已经登进了面板。
`state` 一次性、取走即作废，两条路都守这一条。

解析用户粘回来的东西时不 `url.Parse` 整串：浏览器地址栏里复制的常常没有 scheme
（`127.0.0.1:8080/api/...`），而 `url.Parse` 会把 `127.0.0.1:8080` 当成 scheme，
host 变空、query 丢掉，密钥就找不到了。只在第一个 `?` 处切开、只解析后半段，
带 scheme 和不带 scheme 的都能对。

**上游的错误为什么不能直接 `err.Error()` 显示出去。**
实测过一次：拿一把无效密钥去验，界面上显示的是
`上游返回 401：{"success":false,"error":{"code":"UNAUTHORIZED","status":401,
"message":"Invalid 'Authorization' header or token…`——一段**被截断的 JSON**。
上游明明在 `message` 里写了一句人话。所以 `getJSON` 现在返回
`UpstreamStatusError`，它会先把上游那句话抠出来；`DescribeKeyCheckError` 再把
401/403（换把密钥）和 5xx（重试就行）分开措辞——混为一谈会让用户去换一把好密钥。

**改用户名为什么要当前密码，为什么要换令牌。**
这个系统没有单独的 `username` 字段，登录身份就是 email，所以"改用户名"等于改
登录入口。只凭一个已登录的令牌就允许改，等于把"令牌被偷"升级成"账号被永久
接管"——密码是这道门唯一的把手。改完必须发一把新令牌，因为 email 写在 JWT 声明
里，不换的话本地存的令牌会一直带着旧邮箱。

**账号状态回写一律用条件更新，不做读-改-写。**
`internal/scheduler` 的那一节状态回写（`MarkUsed` / `MarkRateLimited` /
`MarkOverloaded` / `MarkFailure`）都按 `accountID` 条件更新。这不是风格问题：
同一个账号被打回失败时，并发请求会同时进来，"查出来 +1 再写回去"会让两个请求
都读到 N、都写回 N+1，一次失败就这么丢了。

`MarkFailure` 原来正是这么写的——跟它自己那一节的注释自相矛盾。后果是
`consecutive_failures` 明显少算（实测 60 次并发失败只记下 5 次），而**自动禁用
这条兜底恰恰是"没人盯也能停掉坏账号"的唯一保障**，它晚触发甚至不触发，坏账号就
一直留在池子里接着挨撞。现在改成原子自增，禁用判断和写入放进同一条 UPDATE、
条件里带"阈值已越过"且"还没被禁用"，于是那个转换有且只有一次。

这一类 bug 用假的 ent 客户端测不出来——丢更新恰恰是"两个事务真的同时跑"才发生
的。所以 `internal/scheduler/concurrency_test.go` 连真实 PostgreSQL，没设
`CMD2API_TEST_DATABASE_URL` 就跳过并打印跑法。它是验过有效性的：把 `MarkFailure`
还原成读-改-写那版，测试立刻报 `并发记了 60 次失败，计数却是 5`。

顺带修的两处：`HEALTH_FAILURE_THRESHOLD` 配成 0 或负数会让比较式恒真、账号抖一下
就被停掉，构造函数现在钳到 1；`applyAccountPenalty` 里 `IsAuthFailure` 和 default
两个分支代码一模一样，那个 case 只会让查问题的人以为认证失败有特殊处理。

---

## 免责声明

- 本项目仅用于技术学习与研究。
- 使用本项目可能违反 Command Code、Anthropic、OpenAI 或其他上游服务商的**服务条款**。
  请在使用前自行阅读相关协议，风险自负。
- 作者不对账号封禁、服务中断、数据丢失或任何其他直接/间接损失负责。
- 请遵守你所在国家或地区的法律法规。

## 许可证

GNU LGPL-3.0（见 [LICENSE](LICENSE)）。来源与移植范围见 [NOTICE](NOTICE)。
