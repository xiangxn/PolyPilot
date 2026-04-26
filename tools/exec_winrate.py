#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import argparse
import json
from pathlib import Path
from collections import defaultdict, Counter

# 参与“买卖行为”判定的 EXECUTION 状态
ACTION_STATUSES = {"ACCEPTED", "PARTIALLY_FILLED", "FILLED"}


def analyze(log_path: Path):
    # 原始 status+side 统计
    raw_status_side = Counter()
    # 去重后 status+side 统计（仅 ACCEPTED 去重）
    dedup_status_side = Counter()

    # (market_id, token_id, side) 的 ACCEPTED 去重集合
    accepted_seen = set()

    # market -> token -> {"buy": bool, "sell": bool}
    market_token = defaultdict(lambda: defaultdict(lambda: {"buy": False, "sell": False}))

    total_exec = 0

    with log_path.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue

            if obj.get("event") != "EXECUTION":
                continue

            total_exec += 1

            status = str(obj.get("status", "")).upper()
            side = str(obj.get("side", "")).upper()
            market_id = str(obj.get("market_id", ""))
            token_id = str(obj.get("token_id", ""))

            if side not in {"BUY", "SELL"}:
                continue

            raw_status_side[(status, side)] += 1

            # 只对 ACCEPTED 做去重：market+token+side 相同则去重
            if status == "ACCEPTED":
                k = (market_id, token_id, side)
                if k in accepted_seen:
                    continue
                accepted_seen.add(k)

            dedup_status_side[(status, side)] += 1

            # 市场胜负判定只看行为状态
            if status in ACTION_STATUSES and market_id and token_id:
                if side == "BUY":
                    market_token[market_id][token_id]["buy"] = True
                elif side == "SELL":
                    market_token[market_id][token_id]["sell"] = True

    # 计算 market 胜负
    win = 0
    loss = 0
    unresolved = 0

    for market_id, token_map in market_token.items():
        tokens = list(token_map.keys())

        # 有任一 token 出现 sell -> 败
        any_sell = any(token_map[t]["sell"] for t in tokens)
        if any_sell:
            loss += 1
            continue

        # 两个 token 都只有买入没有卖出 -> 胜
        # （等价于：token数>=2，且至少两个 token buy=True 且 sell=False）
        buy_only_cnt = sum(1 for t in tokens if token_map[t]["buy"] and not token_map[t]["sell"])
        if buy_only_cnt >= 2:
            win += 1
        else:
            unresolved += 1

    decided = win + loss
    win_rate = (win / decided) if decided > 0 else 0.0

    return {
        "total_exec": total_exec,
        "raw_status_side": raw_status_side,
        "dedup_status_side": dedup_status_side,
        "market_total": len(market_token),
        "win": win,
        "loss": loss,
        "unresolved": unresolved,
        "win_rate": win_rate,
    }


def print_report(res):
    print("========== EXECUTION统计 ==========")
    print(f"EXECUTION总数: {res['total_exec']}\n")

    print("---- 原始 status+side ----")
    for (status, side), c in sorted(res["raw_status_side"].items()):
        print(f"{status:18s} {side:4s}: {c}")

    print("\n---- 去重后 status+side（仅ACCEPTED去重）----")
    for (status, side), c in sorted(res["dedup_status_side"].items()):
        print(f"{status:18s} {side:4s}: {c}")

    print("\n========== 市场胜负 ==========")
    print(f"market总数: {res['market_total']}")
    print(f"胜(win): {res['win']}")
    print(f"败(loss): {res['loss']}")
    print(f"未定(unresolved): {res['unresolved']}")
    print(f"胜率(win/(win+loss)): {res['win_rate']:.2%}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--log", default="../data/polypilot.20260426.log", help="日志路径")
    args = parser.parse_args()

    log_path = Path(args.log)
    if not log_path.exists():
        raise SystemExit(f"日志不存在: {log_path}")

    res = analyze(log_path)
    print_report(res)


if __name__ == "__main__":
    main()
