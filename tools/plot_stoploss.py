import argparse
import json
from datetime import datetime
import matplotlib.pyplot as plt
import matplotlib.dates as mdates


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--log", default="../data/polypilot.20260426.log", help="日志路径")
    args = parser.parse_args()

    log_path = args.log
    out_path = "../data/stoploss_lz_pd.png"

    times, lz_vals, pd_vals = [], [], []

    with open(log_path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue

            if obj.get("message") != "触发止损":
                continue
            if "LZ" not in obj or "PD" not in obj or "time" not in obj:
                continue

            t = datetime.fromisoformat(obj["time"].replace("Z", "+00:00"))
            times.append(t)
            lz_vals.append(float(obj["LZ"]))
            pd_vals.append(float(obj["PD"]))

    if not times:
        raise SystemExit("未找到触发止损数据")

    fig, ax1 = plt.subplots(figsize=(14, 6))
    ax2 = ax1.twinx()

    ax1.plot(times, lz_vals, color="#1f77b4", linewidth=1.6, label="LZ")
    ax2.plot(times, pd_vals, color="#ff7f0e", linewidth=1.4, alpha=0.9, label="PD")

    ax1.set_xlabel("Time")
    ax1.set_ylabel("LZ", color="#1f77b4")
    ax2.set_ylabel("PD", color="#ff7f0e")
    ax1.tick_params(axis="y", labelcolor="#1f77b4")
    ax2.tick_params(axis="y", labelcolor="#ff7f0e")

    ax1.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))
    ax1.grid(True, linestyle="--", alpha=0.3)

    lines = ax1.get_lines() + ax2.get_lines()
    labels = [l.get_label() for l in lines]
    ax1.legend(lines, labels, loc="upper left")

    plt.title("Stop-loss Events: LZ & PD (2026-04-26)")
    plt.tight_layout()
    plt.savefig(out_path, dpi=150)
    print(f"图已保存: {out_path} ；样本数: {len(times)}")


if __name__ == "__main__":
    main()
