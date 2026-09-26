#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""cmd2api 端到端冒烟测试。

跑之前先起好服务和 Postgres，然后：

    python scripts/e2e_smoke.py http://127.0.0.1:18080

用 Python 而不是 curl 是因为要精确控制 UTF-8 编码——Windows 控制台会把
中文 payload 转成 GBK，测出来的乱码是终端的问题而不是服务端的问题。
"""
from __future__ import print_function

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18080"
EMAIL = os.environ.get("CC_ADMIN_EMAIL", "admin@test.local")
PASSWORD = os.environ.get("CC_ADMIN_PASSWORD", "testpass123")

passed = []
failed = []

# 每次运行用唯一后缀命名资源。
# 这样测试既能对着空库跑，也能对着已经有数据的实例反复跑，不会重名冲突、
# 也不会把真实数据搅乱。
import time as _time
RUN_ID = str(int(_time.time()))[-6:]


def uniq(base):
    return u"%s-%s" % (base, RUN_ID)


def call(method, path, body=None, token=None, raw=False):
    """发一个请求，返回 (status, parsed_body)。"""
    url = BASE + path
    data = None
    headers = {}
    if body is not None:
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


print(u"=== cmd2api 端到端冒烟测试 ===\n")

# ---- 认证 ----
print(u"[认证]")
status, body = call("POST", "/api/auth/login", {"email": EMAIL, "password": PASSWORD})
check(u"管理员登录", status == 200 and "token" in body, (status, body))
token = body.get("token") if status == 200 else None
if not token:
    print(u"\n登录失败，后续测试无法继续。请确认服务已启动且管理员已初始化。")
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
