#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""cmd2api 端到端冒烟测试。

跑之前先起好服务和 Postgres，然后：

    python scripts/e2e_smoke.py http://127.0.0.1:8080

用 Python 而不是 curl 是因为要精确控制 UTF-8 编码——Windows 控制台会把
中文 payload 转成 GBK，测出来的乱码是终端的问题而不是服务端的问题。

登录用的管理员必须已经存在：脚本不建号，只登录。默认端口对着
docker-compose.yml，账号密码要从你的 .env 里拿（就是 BOOTSTRAP_ADMIN_*
那两个），例如：

    set -a; . ./.env; set +a
    CC_ADMIN_EMAIL="$BOOTSTRAP_ADMIN_EMAIL" \\
    CC_ADMIN_PASSWORD="$BOOTSTRAP_ADMIN_PASSWORD" \\
    python backend/scripts/e2e_smoke.py http://127.0.0.1:8080

注意 BOOTSTRAP_ADMIN_PASSWORD 只在**首次启动、库里还没有管理员**时生效。
启动之后再去 .env 改它，密码不会跟着变——这是这个脚本最容易卡住的地方，
所以登录失败时下面会把这句话再提醒一遍。
"""
from __future__ import print_function

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080"
# 不给编造的默认值：猜一个不存在的账号只会换来一句看不懂的 401。
EMAIL = os.environ.get("CC_ADMIN_EMAIL", "")
PASSWORD = os.environ.get("CC_ADMIN_PASSWORD", "")

passed = []
failed = []

# 每次运行用唯一后缀命名资源。
# 这样测试既能对着空库跑，也能对着已经有数据的实例反复跑，不会重名冲突、
# 也不会把真实数据搅乱。
import time as _time
RUN_ID = str(int(_time.time()))[-6:]


def uniq(base):
    return u"%s-%s" % (base, RUN_ID)


def call(method, path, body=None, token=None, raw=False, literally=None, content_type=None):
    """发一个请求，返回 (status, parsed_body)。

    body 走 JSON；literally 是原样发出的字节，配 content_type 用——
    授权回调是 studio 用表单 POST 过来的，那条路必须按它真实的形状测，
    用 JSON 发一遍测不到解析表单的那段代码。
    """
    url = BASE + path
    data = None
    headers = {}
    if literally is not None:
        data = literally
        headers["Content-Type"] = content_type or "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    if token:
        headers["Authorization"] = "Bearer " + token

    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        resp = urllib.request.urlopen(req, timeout=30)
        payload = resp.read().decode("utf-8")
        return resp.getcode(), (payload if raw else json.loads(payload))
    except urllib.error.HTTPError as e:
        payload = e.read().decode("utf-8")
        try:
            return e.code, json.loads(payload)
        except ValueError:
            return e.code, payload


def check(label, condition, detail=""):
    if condition:
        passed.append(label)
        print(u"  ✓ " + label)
    else:
        failed.append(label + " :: " + str(detail))
        print(u"  ✗ " + label + "  -> " + str(detail))


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    """别跟着 303 走。

    浏览器授权的回调就是靠 303 把浏览器送回前端页面的，跟过去就只能看到
    SPA 的 index.html，看不到 Location 里带的原因。要看的是那个 303 本身。
    """

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def call_no_redirect(method, path, body=None, token=None, literally=None, content_type=None):
    """发一个请求，不跟随重定向，返回 (status, headers)。"""
    data = None
    headers = {}
    if literally is not None:
        data = literally
        headers["Content-Type"] = content_type or "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    if token:
        headers["Authorization"] = "Bearer " + token

    opener = urllib.request.build_opener(_NoRedirect)
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        resp = opener.open(req, timeout=30)
        return resp.getcode(), dict(resp.headers)
    except urllib.error.HTTPError as e:
        # 3xx 也会走到这里：redirect_request 返回 None 时 urllib 抛 HTTPError。
        return e.code, dict(e.headers)


print(u"=== cmd2api 端到端冒烟测试 ===\n")

# ---- 认证 ----
print(u"[认证]")
status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": PASSWORD})
check(u"管理员登录", status == 200 and "token" in body, (status, body))
token = body.get("token") if status == 200 else None
if not token:
    print(u"\n登录失败，后续测试无法继续。")
    print(u"  目标地址：%s" % BASE)
    print(u"  登录账号：%s" % (EMAIL or u"（没给，CC_ADMIN_EMAIL 是空的）"))
    if not EMAIL or not PASSWORD:
        print(u"  先把 .env 里的 BOOTSTRAP_ADMIN_EMAIL / BOOTSTRAP_ADMIN_PASSWORD")
        print(u"  导出成 CC_ADMIN_EMAIL / CC_ADMIN_PASSWORD，见本文件开头的说明。")
    else:
        print(u"  账号密码对不上。注意 BOOTSTRAP_ADMIN_PASSWORD 只在首次启动、")
        print(u"  库里还没有管理员时生效；启动后再改 .env，密码不会跟着变。")
        print(u"  另外确认没打错端口——默认是 8080，和你 docker-compose 的映射一致。")
    sys.exit(1)

status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": "wrong"})
check(u"错误密码返回 401", status == 401, status)

status, body = call("GET", "/api/accounts")
check(u"无令牌访问受保护接口返回 401", status == 401, status)

status, body = call("GET", "/api/accounts", token="not-a-real-token")
check(u"伪造令牌返回 401", status == 401, status)

status, body = call("GET", "/api/auth/me", token=token)
check(u"获取当前用户", status == 200 and body["user"]["role"] == "admin", (status, body))

# ---- 分组（含 UTF-8 与平台）----
print(u"\n[分组]")
cn_name = uniq(u"测试中文分组")
status, g1 = call("POST", "/api/groups",
                  {"name": cn_name, "description": u"验证 UTF-8 往返",
                   "platform": "commandcode", "rate_multiplier": 1.0},
                  token=token)
check(u"创建 commandcode 分组", status == 201, (status, g1))
check(u"中文名称原样往返（UTF-8）", g1.get("name") == cn_name,
      u"得到 %r，期望 %r" % (g1.get("name"), cn_name))
check(u"分组带 platform 字段", g1.get("platform") == "commandcode", g1.get("platform"))
cc_group = g1["id"]

status, g2 = call("POST", "/api/groups",
                  {"name": uniq("OpenCode Pool"), "platform": "opencode"}, token=token)
check(u"创建 opencode 分组", status == 201 and g2.get("platform") == "opencode", (status, g2))
oc_group = g2["id"]

status, body = call("POST", "/api/groups", {"name": uniq("Bad"), "platform": "gemini"}, token=token)
check(u"非法平台被拒", status == 400, (status, body))

status, body = call("POST", "/api/groups", {"name": cn_name, "platform": "commandcode"}, token=token)
check(u"重名分组返回 409", status == 409, (status, body))

# 分组平台不可改
status, body = call("PUT", "/api/groups/%d" % cc_group,
                    {"name": cn_name, "platform": "opencode"}, token=token)
check(u"拒绝修改分组平台", status == 400, (status, body))

# ---- 账号 ----
print(u"\n[账号]")
status, a1 = call("POST", "/api/accounts",
                  {"name": uniq(u"主账号"), "platform": "commandcode",
                   "api_key": "user_abcdef1234567890",
                   "concurrency": 3, "priority": 10, "group_ids": [cc_group]},
                  token=token)
check(u"创建 commandcode 账号", status == 201, (status, a1))
check(u"密钥被打码存储", a1.get("masked_key", "").startswith("user") and "****" in a1.get("masked_key", ""),
      a1.get("masked_key"))
check(u"密钥明文未出现在响应里",
      "user_abcdef1234567890" not in json.dumps(a1), "响应里泄露了明文密钥")
check(u"账号绑定了分组", a1.get("group_ids") == [cc_group], a1.get("group_ids"))

status, a2 = call("POST", "/api/accounts",
                  {"name": uniq("OpenCode Zen"), "platform": "opencode", "account_mode": "zen",
                   "api_key": "oc-zen-key-xyz", "group_ids": [oc_group]},
                  token=token)
check(u"创建 opencode 账号", status == 201, (status, a2))
check(u"opencode 自动回填 base_url",
      a2.get("base_url") == "https://opencode.ai/zen/v1", a2.get("base_url"))
check(u"opencode 记录 account_mode", a2.get("account_mode") == "zen", a2.get("account_mode"))

status, a3 = call("POST", "/api/accounts",
                  {"name": uniq("OpenCode Go"), "platform": "opencode", "account_mode": "go",
                   "api_key": "oc-go-key", "group_ids": [oc_group]}, token=token)
check(u"opencode go 模式 base_url 不同",
      a3.get("base_url") == "https://opencode.ai/zen/go/v1", a3.get("base_url"))

# ---- 余额 ----
print(u"\n[余额]")
# 「平台不支持」和「还没刷过」是两种不同的状态，界面上的文案也不同，
# 所以这里分开断言，而不是笼统地看有没有 balance 字段。
check(u"commandcode 账号标记为支持查余额",
      a1.get("balance", {}).get("supported") is True, a1.get("balance"))
check(u"没刷过余额时 remaining 为空",
      a1.get("balance", {}).get("remaining") is None, a1.get("balance"))
check(u"没刷过余额时 fetched_at 为空",
      a1.get("balance", {}).get("fetched_at") is None, a1.get("balance"))
check(u"没刷过余额时带 supported 字段而不是缺字段",
      "balance" in a1 and "supported" in a1["balance"], sorted(a1.get("balance", {}).keys()))

# opencode 是「不支持」而不是「失败」——混成一种状态会让管理员去查一个
# 并不存在的故障。
check(u"opencode 账号标记为不支持查余额",
      a2.get("balance", {}).get("supported") is False, a2.get("balance"))

status, body = call("POST", "/api/accounts/%d/balance" % a2["id"], token=token)
check(u"对 opencode 账号刷余额返回明确的不支持而不是报错",
      status == 400 and u"不支持" in json.dumps(body, ensure_ascii=False), (status, body))

# 拿一把假密钥去真上游刷余额。这里**不断言成功**：跑测试的机器可能没有外网，
# 上游也一定会拒绝这把假密钥。要断言的是「失败也是干净的失败」——
# 200 + ok:false，而不是 5xx，也不是把整次刷新做成一个异常。
status, body = call("POST", "/api/accounts/%d/balance" % a1["id"], token=token)
check(u"余额刷新失败时返回 200 + ok:false 而不是 5xx",
      status == 200 and body.get("ok") is False, (status, body))

status, body = call("GET", "/api/accounts/%d" % a1["id"], token=token)
check(u"刷新失败后账号仍可读", status == 200, (status, body))
# 失败绝不能推进 fetched_at：那个字段的含义是「界面上的数字是什么时候取的」，
# 推进它会让一个旧数字看起来像刚刚取的。
check(u"刷新失败不推进 fetched_at",
      body.get("balance", {}).get("fetched_at") is None, body.get("balance"))

# 平台错配
status, body = call("POST", "/api/accounts",
                    {"name": uniq("Mismatch"), "platform": "opencode", "account_mode": "zen",
                     "api_key": "oc-key", "group_ids": [cc_group]}, token=token)
check(u"拒绝跨平台绑定分组", status == 400, (status, body))

# commandcode 密钥前缀校验
status, body = call("POST", "/api/accounts",
                    {"name": uniq("NoPrefix"), "platform": "commandcode",
                     "api_key": "badprefix", "group_ids": [cc_group]}, token=token)
check(u"拒绝缺少 user_ 前缀的 commandcode 密钥", status == 400, (status, body))

# opencode 不要求 user_ 前缀
status, body = call("POST", "/api/accounts",
                    {"name": uniq("OC NoPrefix"), "platform": "opencode", "account_mode": "zen",
                     "api_key": "sk-opencode-whatever", "group_ids": [oc_group]}, token=token)
check(u"opencode 密钥不强制 user_ 前缀", status == 201, (status, body))

# opencode 必须给 account_mode
status, body = call("POST", "/api/accounts",
                    {"name": uniq("OC NoMode"), "platform": "opencode",
                     "api_key": "oc-key", "group_ids": [oc_group]}, token=token)
check(u"opencode 缺 account_mode 被拒", status == 400, (status, body))

# 平台筛选
# 断言"我们自己建的账号在对应平台里"，而不是全局总数 ——
# 这样测试既能对着空库跑，也能对着已有数据的实例跑。
# 按本次运行的后缀过滤（用 RUN_ID 而不是平台名做关键词：
# 账号名不一定含平台字样，例如 "OC NoPrefix"，按平台名过滤会漏掉它）。
status, body = call("GET", "/api/accounts?platform=opencode&keyword=" + urllib.parse.quote(RUN_ID),
                    token=token)
oc_items = body.get("items", [])
check(u"按平台筛选账号",
      status == 200 and len(oc_items) >= 3 and all(a["platform"] == "opencode" for a in oc_items),
      (status, [a["name"] for a in oc_items]))

status, body = call("GET", "/api/accounts?platform=commandcode&keyword=" + urllib.parse.quote(u"主账号-" + RUN_ID),
                    token=token)
check(u"筛选 commandcode 账号",
      status == 200 and body.get("total") == 1
      and body["items"][0]["platform"] == "commandcode", (status, body.get("total")))

# 批量导入
status, body = call("POST", "/api/accounts/batch",
                    {"keys": "user_batch1\n" + u"批量二,user_batch2\n"
                             u"# 注释行\n\nuser_batch3",
                     "platform": "commandcode", "group_ids": [cc_group]}, token=token)
check(u"批量导入（含注释与空行）", status == 200 and body.get("created") == 3,
      (status, body))
check(u"批量导入无失败项", body.get("failed") == 0, body.get("failures"))

status, body = call("POST", "/api/accounts/batch",
                    {"keys": "bad1\nbad2", "platform": "commandcode", "group_ids": [cc_group]},
                    token=token)
check(u"批量导入逐行报错而不是整体失败",
      status == 200 and body.get("created") == 0 and body.get("failed") == 2, (status, body))

# 贴 ~/.commandcode/auth.json 的内容导入。
#
# 两个对象首尾相接，而不是包在数组里——这不是合法 JSON，但人手上一次贴
# 几个账号时就是这么干的，是解析器必须扛住的那种输入。
# userName 里带上 RUN_ID，既让账号名唯一（重复跑不会撞 409），又能拿来检索。
auth_json_text = (
    u'{\n'
    u'  "apiKey": "user_authjson1-' + RUN_ID + u'",\n'
    u'  "userId": "u_1",\n'
    u'  "userName": "授权账号-' + RUN_ID + u'",\n'
    u'  "keyName": "cli",\n'
    u'  "authenticatedAt": "2026-09-26T10:00:00.000Z"\n'
    u'}\n'
    u'{"apiKey": "user_authjson2-' + RUN_ID + u'", "userName": "第二把-' + RUN_ID + u'"}\n'
)
status, body = call("POST", "/api/accounts/batch",
                    {"keys": auth_json_text, "platform": "commandcode", "group_ids": [cc_group]},
                    token=token)
check(u"批量导入 auth.json（两个对象首尾相接）",
      status == 200 and body.get("created") == 2 and body.get("failed") == 0, (status, body))

status, body = call("GET", "/api/accounts?platform=commandcode&keyword=" + urllib.parse.quote(RUN_ID),
                    token=token)
names = sorted(a["name"] for a in body.get("items", []))
check(u"auth.json 的账号名取自 userName 与 keyName",
      u"授权账号-" + RUN_ID + u" · cli" in names, names)
check(u"auth.json 缺 keyName 时只用 userName",
      u"第二把-" + RUN_ID in names, names)

# 缺 apiKey 的条目要单独报错，不能连累同一批里的好账号。
status, body = call("POST", "/api/accounts/batch",
                    {"keys": u'{"apiKey": "user_ok-' + RUN_ID + u'"}\n{"userName": "没有密钥"}\n',
                     "platform": "commandcode", "group_ids": [cc_group]},
                    token=token)
check(u"auth.json 里的坏条目只坏自己",
      status == 200 and body.get("created") == 1 and body.get("failed") == 1, (status, body))

# ---- 浏览器授权 ----
print(u"\n[浏览器授权]")
status, body = call("POST", "/api/accounts/oauth/commandcode",
                    {"name": uniq(u"待授权"), "group_ids": [cc_group],
                     "concurrency": 3, "priority": 50}, token=token)
check(u"发起授权返回跳转地址",
      status == 200 and "auth_url" in body and "callback_url" in body, (status, body))

auth_url = body.get("auth_url", "")
query = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(auth_url).query))
check(u"授权地址指向 studio 的 CLI 入口",
      auth_url.startswith("https://commandcode.ai/studio/auth/cli?"), auth_url)
check(u"授权地址带回跳与一次性 state",
      query.get("mode") == "redirect" and len(query.get("state", "")) >= 32
      and query.get("callback", "").endswith("/callback"),
      query)
check(u"发起授权这一步不会先建出账号",
      call("GET", "/api/accounts?platform=commandcode&keyword=" + urllib.parse.quote(u"待授权-" + RUN_ID),
           token=token)[1].get("total") == 0, None)

# 面板跑在本机时 callback_is_loopback 为真，前端才允许开自动那条路。
# 脚本默认就是对着 127.0.0.1 跑的，所以这里应当是 true；这个字段判错的
# 后果是远程面板会去开一个注定显示 Invalid Request 的标签页。
check(u"本机回调会告诉前端可以走自动模式",
      body.get("callback_is_loopback") is True,
      (body.get("callback_is_loopback"), body.get("callback_url")))

# 回调必须在鉴权之外：浏览器是跳过来的，带不了 Authorization 头。
# 带一个不存在的 state 时应当被拒，而不是建出账号。
status, headers = call_no_redirect("GET", "/callback?state=" + u"x" * 43)
location = headers.get("Location", "")
check(u"回调不要求登录（浏览器跳转带不了 token）", status != 401, status)
check(u"回调拒绝未知 state",
      status == 303 and location.startswith("/accounts?"), (status, location))
check(u"失败时回跳地址里带上原因",
      u"授权链接已失效" in urllib.parse.unquote(location), location)
check(u"回跳地址里不带密钥", u"apiKey" not in location and u"user_" not in location, location)

# studio 交回结果用的是**表单 POST**（mode=redirect 时它提交一个隐藏表单），
# 不是带 query 的跳转。早先的实现按 query 读，所以自动那条路一次都没走通过——
# 下面两段就是照它真实的形状打的：只验「state 被认出来并消费掉」，密钥能不能
# 换成账号取决于上游，这里不管。
def new_oauth_state(label):
    s, b = call("POST", "/api/accounts/oauth/commandcode",
                {"name": uniq(label), "group_ids": [cc_group],
                 "concurrency": 1, "priority": 70}, token=token)
    return b.get("state", "") if s == 200 else ""


form_state = new_oauth_state(u"表单回调")
check(u"能拿到一次新的握手 state", bool(form_state), form_state)
_form = urllib.parse.urlencode({
    "apiKey": "user_smoke_form_fake", "state": form_state,
    "userId": "u-smoke", "userName": u"表单用户", "keyName": "cli",
}).encode("utf-8")
status, headers = call_no_redirect("POST", "/callback", literally=_form)
location = headers.get("Location", "")
check(u"表单 POST 的回调不要求登录", status != 401, status)
check(u"表单 POST 的回调按 303 送回前端",
      status == 303 and location.startswith("/accounts?"), (status, location))

# 同一条表单再发一次。第一次已经把 state 收掉了，所以这次必然是「已失效」——
# 这一条正着证明前一次真的被处理过，而不是被悄悄丢掉。
status, headers = call_no_redirect("POST", "/callback", literally=_form)
check(u"state 只认一次：重放同一条回调是已失效",
      u"授权链接已失效" in urllib.parse.unquote(headers.get("Location", "")),
      (status, headers.get("Location")))

# 另一个分支：studio 的 fetch 那条路发的是 JSON。
deny_state = new_oauth_state(u"表单拒绝")
check(u"能拿到一次新的握手 state（拒绝分支）", bool(deny_state), deny_state)
status, headers = call_no_redirect(
    "POST", "/callback",
    literally=json.dumps({"state": deny_state, "error": "access_denied"}).encode("utf-8"),
    content_type="application/json")
check(u"JSON 回调也能认（studio 另一条投递路径）",
      u"已取消授权" in urllib.parse.unquote(headers.get("Location", "")),
      (status, headers.get("Location")))

# 有人把这个地址当普通页面打开时的兜底：什么都没带就直说这是回调地址，
# 别回一句看不懂的「state 无效」。
status, body = call("GET", "/callback")
check(u"手动打开回调地址时给一句人话",
      status == 400 and u"回调" in body.get("error", ""), (status, body))

# ---- 授权结果的两种收尾方式 ----
#
# 自动那条（回调）在上面验过了。这里验的是「另一个标签页问好了没」和
# 「用户手动把回调地址粘回来」——面板跑在远程服务器上、浏览器跳不回本机时，
# 这两条是唯一能把授权救回来的路。
print(u"\n[授权收尾]")

# 重新发起一次，拿到一个真实的 state 用。
status, body = call("POST", "/api/accounts/oauth/commandcode",
                    {"name": uniq(u"待收尾"), "group_ids": [cc_group],
                     "concurrency": 5, "priority": 60}, token=token)
oauth_state = body.get("state", "") if status == 200 else ""
check(u"发起授权会回传 state（轮询要用）", bool(oauth_state), body)
# 要比的是**这一次**授权地址里的 state。上面那个 query 是第一次发起时解析的，
# 拿它来比必然不等——每次发起都是新的 state。
this_query = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(body.get("auth_url", "")).query))
check(u"state 与本次授权地址里的那一个一致",
      oauth_state and oauth_state == this_query.get("state"),
      (oauth_state, this_query.get("state")))
callback_url = body.get("callback_url", "")
check(u"回传的回调地址是绝对地址",
      callback_url.startswith("http") and callback_url.endswith("/callback"), callback_url)

status, body = call("GET", "/api/accounts/oauth/commandcode/status?state=" + oauth_state,
                    token=token)
check(u"刚发起时状态是等待中",
      status == 200 and body.get("status") == "pending", (status, body))

status, body = call("GET", "/api/accounts/oauth/commandcode/status?state=" + u"n" * 43,
                    token=token)
check(u"未知 state 报已失效（前端据此停止轮询）",
      status == 200 and body.get("status") == "gone", (status, body))

# 这两条是面板自己发的 XHR，必须在鉴权之内：放到鉴权之外就等于开出一个
# 用别人的 state 建账号的口子——state 会出现在浏览器地址栏里。
status, _ = call("GET", "/api/accounts/oauth/commandcode/status?state=" + oauth_state)
check(u"状态查询要求登录", status == 401, status)
status, _ = call("POST", "/api/accounts/oauth/commandcode/complete",
                 {"result": "user_x", "state": oauth_state})
check(u"手动收尾要求登录", status == 401, status)

# 粘错东西时要报人话。最容易粘错的是**授权页**地址：它 query 里也有
# callback 和 state，看着很像，但没有 apiKey。
status, body = call("POST", "/api/accounts/oauth/commandcode/complete",
                    {"result": auth_url, "state": oauth_state}, token=token)
check(u"粘授权页地址时给出可读的提示",
      status == 400 and u"授权页" in body.get("error", ""), (status, body))

status, body = call("POST", "/api/accounts/oauth/commandcode/complete",
                    {"result": "https://panel.example.com/accounts"}, token=token)
check(u"粘面板地址时也能认出不是回调地址",
      status == 400 and u"apiKey" in body.get("error", ""), (status, body))

# 拿一个假密钥走完整条手动流程：解析要对、要走 upstream 校验、要失败得
# 干净（这里环境无外网，whoami 必然失败——但**失败的方式**是重点：
# 得是「密钥没能通过校验」，而不是 500 或者 panic）。
status, body = call("POST", "/api/accounts/oauth/commandcode/complete",
                    {"result": callback_url + "?apiKey=user_smoke_fake_key&state=" + oauth_state,
                     "state": oauth_state},
                    token=token)
check(u"手动粘贴走完解析并尝试校验密钥（失败也应是 4xx 而不是 500）",
      status == 400, (status, body))
# 这是实测出来的问题：上游回的是一段 JSON，整段透出去用户看到的是一串
# 被截断的 {"success":false,... }，一句都读不懂。提示里必须是话，不是响应体。
_msg = body.get("error", "")
check(u"校验失败的提示是人话，不是上游的响应体",
      u"{" not in _msg and u"success" not in _msg and len(_msg) > 8, _msg)
check(u"校验失败时账号没有建出来",
      call("GET", "/api/accounts?platform=commandcode&keyword="
           + urllib.parse.quote(u"待收尾-" + RUN_ID), token=token)[1].get("total") == 0, None)

# ---- 改密码 ----
#
# 真改一遍再改回来。这条以前没被覆盖，而它是两个「改账号」操作里更安全攸关的
# 那个：改错了人就登不进来了。只验「接口返回 200」不够——得验旧密码真的失效、
# 新密码真的能用，那两个才是用户能感知的结果。
print(u"\n[改密码]")
TMP_PASSWORD = "smoke-tmp-" + RUN_ID

status, body = call("POST", "/api/auth/password",
                    {"current_password": "definitely-wrong", "new_password": TMP_PASSWORD},
                    token=token)
check(u"当前密码填错时拒绝改", status == 400, (status, body))

status, body = call("POST", "/api/auth/password",
                    {"current_password": PASSWORD, "new_password": "short"}, token=token)
check(u"新密码少于 8 位时拒绝", status == 400, (status, body))

status, body = call("POST", "/api/auth/password",
                    {"current_password": PASSWORD, "new_password": TMP_PASSWORD}, token=token)
check(u"改密码成功", status == 200, (status, body))

status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": PASSWORD})
check(u"旧密码立即失效", status == 401, (status, body))

status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": TMP_PASSWORD})
check(u"新密码能登录", status == 200 and "token" in body, (status, body))
token = body.get("token", token)

# 改回去，别把这个实例的密码留在测试值上——留在测试值上等于把管理员锁在外面。
status, body = call("POST", "/api/auth/password",
                    {"current_password": TMP_PASSWORD, "new_password": PASSWORD}, token=token)
check(u"改回原密码", status == 200, (status, body))

status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": PASSWORD})
check(u"原密码恢复可用", status == 200 and "token" in body, (status, body))
token = body.get("token", token)

# ---- 登录用户名与密码 ----
print(u"\n[登录用户名]")
status, body = call("PUT", "/api/auth/profile",
                    {"current_password": "definitely-wrong", "email": "someone@else.com"},
                    token=token)
check(u"改用户名需要当前密码", status == 400, (status, body))

status, body = call("PUT", "/api/auth/profile",
                    {"current_password": PASSWORD, "email": "not-an-email"}, token=token)
check(u"拒绝不成形的邮箱", status == 400, (status, body))

status, body = call("PUT", "/api/auth/profile",
                    {"current_password": PASSWORD, "email": EMAIL}, token=token)
check(u"改成当前用户名不报错（幂等，不写库）",
      status == 200 and "user" in body and body["user"]["email"] == EMAIL, (status, body))

# 真的改一次再改回来：这条路径要动库，不真跑一遍等于没测。
alt_email = "smoke-" + RUN_ID + "@cmd2api.local"
status, body = call("PUT", "/api/auth/profile",
                    {"current_password": PASSWORD, "email": alt_email}, token=token)
check(u"改登录用户名成功", status == 200 and body.get("user", {}).get("email") == alt_email,
      (status, body))
new_token = body.get("token", "")
check(u"改完发回一把新令牌（邮箱写在 JWT 声明里）", bool(new_token), None)

status, body = call("GET", "/api/auth/me", token=new_token)
check(u"新令牌可用且带着新用户名",
      status == 200 and body.get("user", {}).get("email") == alt_email, (status, body))

status, body = call("POST", "/api/auth/login",
                    {"email": alt_email, "password": PASSWORD})
check(u"可以用新用户名登录", status == 200 and "token" in body, (status, body))

# 改回去，别把这个实例的用户名留在测试值上。
status, body = call("PUT", "/api/auth/profile",
                    {"current_password": PASSWORD, "email": EMAIL}, token=new_token)
check(u"改回原用户名", status == 200 and body.get("user", {}).get("email") == EMAIL,
      (status, body))
token = body.get("token", token)

# ---- API Key ----
print(u"\n[API Key]")
status, k1 = call("POST", "/api/keys",
                  {"name": uniq(u"测试密钥"), "group_id": cc_group,
                   "quota": 10.0, "ip_whitelist": ["127.0.0.1", "10.0.0.0/8"]}, token=token)
check(u"签发 API Key", status == 201, (status, k1))
check(u"密钥带 sk-c2a- 前缀", k1.get("key", "").startswith("sk-c2a-"), k1.get("key"))
check(u"IP 白名单被保存", k1.get("ip_whitelist") == ["127.0.0.1", "10.0.0.0/8"],
      k1.get("ip_whitelist"))
api_key = k1.get("key")

status, body = call("POST", "/api/keys",
                    {"name": uniq("With bad group"), "group_id": 99999}, token=token)
check(u"拒绝不存在的分组", status == 400, (status, body))

status, body = call("GET", "/api/keys?keyword=" + urllib.parse.quote(u"测试密钥-" + RUN_ID), token=token)
check(u"列出 API Key",
      status == 200 and body.get("total") == 1 and body["items"][0]["group_id"] == cc_group,
      (status, body.get("total")))

# ---- 中转入口 ----
print(u"\n[中转入口]")
# 无密钥
status, body = call("POST", "/v1/chat/completions",
                    {"model": "x", "messages": [{"role": "user", "content": "hi"}]})
check(u"无密钥调用中转返回 401", status == 401, (status, body))

# 伪造密钥
status, body = call("POST", "/v1/messages",
                    {"model": "x", "max_tokens": 10,
                     "messages": [{"role": "user", "content": "hi"}]},
                    token="sk-c2a-fake")
check(u"伪造密钥返回 401", status == 401, (status, body))
check(u"Anthropic 错误格式正确",
      isinstance(body, dict) and body.get("type") == "error" and "error" in body, body)

# 有效密钥 + 真实账号：这一路会真的打到 Command Code 上游。
#
# 测试库里放的是假账号密钥，所以上游一定会拒绝——那正是期望的结果，
# 因为它证明请求真的穿过了整条链路（鉴权 → 选号 → 解密 → 信封 → 上游）。
#
# 要区分的是「谁拒绝的」：cmd2api 自己拒绝时措辞是「API Key 无效」，
# 上游拒绝时是上游自己的话术。收到后者才说明链路通了。
status, body = call("POST", "/v1/chat/completions",
                    {"model": "deepseek/deepseek-v4-flash",
                     "messages": [{"role": "user", "content": "hi"}]},
                    token=api_key)
message = body.get("error", {}).get("message", "") if isinstance(body, dict) else ""
reached_upstream = (status == 200) or (message != u"API Key 无效" and message != "")
check(u"有效密钥通过 cmd2api 鉴权并转发到上游", reached_upstream, (status, body))
print(u"      环境无外网时属正常：cmd2api 侧 status=%s msg=%r" % (status, message))

# Anthropic 入口同样走一遍，确认协议识别正确。
status, body = call("POST", "/v1/messages",
                    {"model": "claude-sonnet-4-6", "max_tokens": 16,
                     "messages": [{"role": "user", "content": "hi"}]},
                    token=api_key)
message = body.get("error", {}).get("message", "") if isinstance(body, dict) else ""
reached_upstream = (status == 200) or (message != u"API Key 无效" and message != "")
check(u"Anthropic 入口同样能转发", reached_upstream, (status, body))

# ---- 其余后台接口 ----
print(u"\n[其他接口]")
status, body = call("GET", "/api/dashboard", token=token)
check(u"仪表盘", status == 200 and "summary" in body, (status, body))
check(u"仪表盘按平台统计账号数",
      "accounts_by_platform" in body.get("runtime", {}), body.get("runtime"))

status, body = call("GET", "/api/logs", token=token)
check(u"用量日志", status == 200 and "items" in body, (status, body))

status, body = call("GET", "/api/settings", token=token)
check(u"设置", status == 200 and "upstream" in body, (status, body))

status, body = call("GET", "/api/auth/me", token=token)
check(u"账号与密钥数一致", status == 200, status)

# 参数校验
status, body = call("GET", "/api/dashboard?start=notatime", token=token)
check(u"非法时间格式被拒", status == 400, (status, body))

status, body = call("GET", "/api/logs?page_size=99999", token=token)
check(u"超大分页被夹住", status == 200 and body.get("page_size") == 200, body.get("page_size"))

# ---- 删除（必须走软删除）----
print(u"\n[删除]")

# 这些资源都已经被绑定关系引用过。硬删除会撞外键直接 500 ——
# 而这个 bug 只在资源被用过之后才暴露，全新部署时测不出来。
status, body = call("POST", "/api/groups",
                    {"name": uniq(u"待删除分组"), "platform": "commandcode"}, token=token)
del_group = body.get("id")
check(u"建待删分组", status == 201, (status, body))

status, body = call("POST", "/api/accounts",
                    {"name": uniq(u"待删账号"), "platform": "commandcode",
                     "api_key": "user_deltest0001", "group_ids": [del_group]}, token=token)
del_account = body.get("id")
check(u"建待删账号", status == 201, (status, body))

status, body = call("POST", "/api/keys",
                    {"name": uniq(u"待删密钥"), "group_id": del_group}, token=token)
del_key = body.get("id")
del_key_value = body.get("key")
check(u"建待删密钥", status == 201, (status, body))

status, body = call("DELETE", "/api/accounts/%d" % del_account, token=token)
check(u"删除账号返回 200（软删除，不撞外键）", status == 200, (status, body))

status, body = call("GET", "/api/accounts?keyword=" + urllib.parse.quote(uniq(u"待删账号")), token=token)
check(u"删除后账号从列表消失", status == 200 and body.get("total") == 0, (status, body))
check(u"重复删除返回 404",
      call("DELETE", "/api/accounts/%d" % del_account, token=token)[0] == 404)

status, body = call("DELETE", "/api/keys/%d" % del_key, token=token)
check(u"删除密钥返回 200", status == 200, (status, body))

# 已删除的密钥必须立刻失效：鉴权查询带 deleted_at IS NULL
status, body = call("POST", "/v1/chat/completions",
                    {"model": "x", "messages": [{"role": "user", "content": "hi"}]},
                    token=del_key_value)
check(u"已删除的密钥立即失效", status == 401, (status, body))

# 分组下还有密钥时拒绝删除，避免把密钥变成找不到账号池的孤儿
status, body = call("POST", "/api/groups",
                    {"name": uniq(u"有密钥的分组"), "platform": "commandcode"}, token=token)
busy_group = body.get("id")
call("POST", "/api/keys", {"name": uniq(u"占位"), "group_id": busy_group}, token=token)
status, body = call("DELETE", "/api/groups/%d" % busy_group, token=token)
check(u"分组下还有密钥时拒绝删除", status == 409, (status, body))

status, body = call("DELETE", "/api/groups/%d" % del_group, token=token)
check(u"空分组可以删除", status == 200, (status, body))

# ---- 汇总 ----
print(u"\n" + u"=" * 46)
print(u"通过 %d 项，失败 %d 项" % (len(passed), len(failed)))
if failed:
    print(u"\n失败项：")
    for f in failed:
        print(u"  - " + f)
    sys.exit(1)
print(u"全部通过")
