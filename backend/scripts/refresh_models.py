#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从本地装的 command-code 包里刷出 hardcodedModels 那张兜底表。

    python backend/scripts/refresh_models.py            # 打印 Go 代码块
    python backend/scripts/refresh_models.py --write    # 直接改 client.go

CLI 没有文档，模型目录只能从包体里读：dist/cli.mjs 里有一张
{SONNET_5:{id:"...",name:"...",...}, ...} 的字面量表，就是命令行的模型选择器
赖以工作的那张表。这个脚本把它的 id/name 抠出来，按上游声明顺序输出。

两个坑：

1. 不能只用正则找 "id"。"hidden" 有两种写法——字面量 hidden:!0，以及
   get hidden(){return isLingFlashFreeEnded()} 这种 getter（促销结束就自动
   隐藏）。只认字面量会把免费模型当成正式模型抄进去。
2. 表里的条目是**扁平的**，没有嵌套对象，所以按花括号配对切顶层条目是安全的。
   别改成找最长的字符串数组——包体里更长的 id 形状数组是 coreutils 命令名。

包体路径默认取 npm 全局前缀；换机器、换 nvm 版本时用 --bundle 指定。
"""

import argparse
import io
import json
import os
import re
import subprocess
import sys

DEFAULT_REL = "node_modules/command-code/dist/cli.mjs"


def find_bundle(explicit):
    if explicit:
        return explicit
    try:
        # shell=True 是必须的：Windows 上 npm 是 npm.cmd，CreateProcess 直接起不了。
        prefix = subprocess.check_output(
            "npm prefix -g", shell=True, stderr=subprocess.STDOUT
        ).decode("utf-8", "replace").strip()
    except (OSError, subprocess.CalledProcessError):
        prefix = ""
    if prefix:
        cand = os.path.join(prefix, DEFAULT_REL)
        if os.path.isfile(cand):
            return cand
    raise SystemExit("找不到 cli.mjs，用 --bundle 指定路径")


def read_bundle(path):
    with io.open(path, encoding="utf-8", errors="replace") as f:
        return f.read()


def match_braces(s, start):
    """从 s[start] == '{' 出发，返回配对 '}' 的下标（含）。

    字符串里的花括号要跳过，否则描述文本里的 '{name}' 会把深度算错——那是真的
    会出事的：表里好几条 notice 都写着 "{name} is free and uses shared
    capacity"。反引号也当字符串起止（模板字面量），漏掉的话后面整段都会被
    当成字符串吃掉。
    """
    depth = 0
    i = start
    quote = None
    while i < len(s):
        c = s[i]
        if quote:
            if c == "\\":
                i += 2
                continue
            if c == quote:
                quote = None
        elif c in "\"'`":
            quote = c
        elif c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise SystemExit("花括号没配对，包体结构变了？")


def find_catalog(s, anchor):
    """定位整张模型表。

    锚点是某条模型的 id，它落在**条目自己的** '{' 里面，而我们要的是外面那张
    表的 '{'。这个区别踩过一次：直接拿条目花括号配对，切出来的只有一条模型，
    脚本还"成功"跑完了。所以先找到条目的 '{'，再往外一层。
    """
    entry_brace = s.rindex("{", 0, s.index(anchor))
    table_brace = s.rindex("{", 0, entry_brace)
    return s[table_brace:match_braces(s, table_brace) + 1]


def split_entries(body):
    """把 {A:{...},B:{...}} 切成每个条目的文本。"""
    out = []
    depth = 0
    quote = None
    j = 1
    cur = None
    while j < len(body):
        c = body[j]
        if quote:
            if c == "\\":
                j += 2
                continue
            if c == quote:
                quote = None
        elif c in "\"'`":
            quote = c
        elif c == "{":
            depth += 1
            if depth == 1:
                cur = j
        elif c == "}":
            if depth == 1 and cur is not None:
                out.append(body[cur:j + 1])
            depth -= 1
        j += 1
    return out


def extract(bundle_path):
    s = read_bundle(bundle_path)
    # 用一条模型 id 当锚点定位那张表，比找变量名稳（变量名会被压缩器改）。
    body = find_catalog(s, 'id:"claude-sonnet-5"')

    models = []
    for entry in split_entries(body):
        m_id = re.search(r'\bid:"([^"]+)"', entry)
        if not m_id:
            continue
        m_name = re.search(r'\bname:"([^"]+)"', entry)
        if not m_name:
            continue  # 没名字的条目不是给选择器用的
        # hidden 的两种形态都要认，见模块 docstring。
        if re.search(r'\bhidden\s*[:(]', entry):
            continue
        models.append((m_id.group(1), m_name.group(1)))

    if len(models) < 20:
        raise SystemExit("只抠到 %d 条，包体结构大概变了，先人工看一眼" % len(models))
    return models


def go_block(models):
    lines = ["var hardcodedModels = []Model{"]
    for mid, name in models:
        lines.append("\t{ID: %s, Name: %s}," % (
            json.dumps(mid, ensure_ascii=False),
            json.dumps(name, ensure_ascii=False),
        ))
    lines.append("}")
    return "\n".join(lines)


def write_into_client(go_path, block):
    with io.open(go_path, encoding="utf-8", newline="") as f:
        s = f.read()
    start = s.index("var hardcodedModels = []Model{")
    end = s.index("\n}\n", start) + len("\n}\n")
    with io.open(go_path, "w", encoding="utf-8", newline="") as f:
        f.write(s[:start] + block + "\n" + s[end:])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bundle", help="cli.mjs 的路径")
    ap.add_argument("--write", action="store_true",
                    help="直接改 backend/internal/relay/client.go")
    args = ap.parse_args()

    path = find_bundle(args.bundle)
    models = extract(path)
    block = go_block(models)

    if args.write:
        repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        go_path = os.path.join(repo, "internal", "relay", "client.go")
        write_into_client(go_path, block)
        sys.stderr.write("已写入 %s（%d 条），记得 gofmt 一下\n" % (go_path, len(models)))
    else:
        sys.stdout.write(block + "\n")


if __name__ == "__main__":
    main()
