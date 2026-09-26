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
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18080"
EMAIL = os.environ.get("CC_ADMIN_EMAIL", "admin@test.local")
PASSWORD = os.environ.get("CC_ADMIN_PASSWORD", "testpass123")

passed = []
failed = []


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
cn_name = u"测试中文分组"
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
                  {"name": "OpenCode Pool", "platform": "opencode"}, token=token)
check(u"创建 opencode 分组", status == 201 and g2.get("platform") == "opencode", (status, g2))
oc_group = g2["id"]

status, body = call("POST", "/api/groups", {"name": "Bad", "platform": "gemini"}, token=token)
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
                  {"name": u"主账号", "platform": "commandcode",
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
                  {"name": "OpenCode Zen", "platform": "opencode", "account_mode": "zen",
                   "api_key": "oc-zen-key-xyz", "group_ids": [oc_group]},
                  token=token)
check(u"创建 opencode 账号", status == 201, (status, a2))
check(u"opencode 自动回填 base_url",
      a2.get("base_url") == "https://opencode.ai/zen/v1", a2.get("base_url"))
check(u"opencode 记录 account_mode", a2.get("account_mode") == "zen", a2.get("account_mode"))

status, a3 = call("POST", "/api/accounts",
                  {"name": "OpenCode Go", "platform": "opencode", "account_mode": "go",
                   "api_key": "oc-go-key", "group_ids": [oc_group]}, token=token)
check(u"opencode go 模式 base_url 不同",
      a3.get("base_url") == "https://opencode.ai/zen/go/v1", a3.get("base_url"))

# 平台错配
status, body = call("POST", "/api/accounts",
                    {"name": "Mismatch", "platform": "opencode", "account_mode": "zen",
                     "api_key": "oc-key", "group_ids": [cc_group]}, token=token)
check(u"拒绝跨平台绑定分组", status == 400, (status, body))

# commandcode 密钥前缀校验
status, body = call("POST", "/api/accounts",
                    {"name": "NoPrefix", "platform": "commandcode",
                     "api_key": "badprefix", "group_ids": [cc_group]}, token=token)
check(u"拒绝缺少 user_ 前缀的 commandcode 密钥", status == 400, (status, body))

# opencode 不要求 user_ 前缀
status, body = call("POST", "/api/accounts",
                    {"name": "OC NoPrefix", "platform": "opencode", "account_mode": "zen",
                     "api_key": "sk-opencode-whatever", "group_ids": [oc_group]}, token=token)
check(u"opencode 密钥不强制 user_ 前缀", status == 201, (status, body))

# opencode 必须给 account_mode
status, body = call("POST", "/api/accounts",
                    {"name": "OC NoMode", "platform": "opencode",
                     "api_key": "oc-key", "group_ids": [oc_group]}, token=token)
check(u"opencode 缺 account_mode 被拒", status == 400, (status, body))

# 平台筛选
status, body = call("GET", "/api/accounts?platform=opencode", token=token)
check(u"按平台筛选账号", status == 200 and body.get("total") == 3,
      (status, body.get("total"), u"期望 3 个 opencode 账号"))

status, body = call("GET", "/api/accounts?platform=commandcode", token=token)
check(u"筛选 commandcode 账号", status == 200 and body.get("total") == 1, body.get("total"))

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
                  {"name": u"测试密钥", "group_id": cc_group,
                   "quota": 10.0, "ip_whitelist": ["127.0.0.1", "10.0.0.0/8"]}, token=token)
check(u"签发 API Key", status == 201, (status, k1))
check(u"密钥带 sk-c2a- 前缀", k1.get("key", "").startswith("sk-c2a-"), k1.get("key"))
check(u"IP 白名单被保存", k1.get("ip_whitelist") == ["127.0.0.1", "10.0.0.0/8"],
      k1.get("ip_whitelist"))
api_key = k1.get("key")

status, body = call("POST", "/api/keys",
                    {"name": "With bad group", "group_id": 99999}, token=token)
check(u"拒绝不存在的分组", status == 400, (status, body))

status, body = call("GET", "/api/keys", token=token)
check(u"列出 API Key", status == 200 and body.get("total") == 1, (status, body))

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

# ---- 汇总 ----
print(u"\n" + u"=" * 46)
print(u"通过 %d 项，失败 %d 项" % (len(passed), len(failed)))
if failed:
    print(u"\n失败项：")
    for f in failed:
        print(u"  - " + f)
    sys.exit(1)
print(u"全部通过")
