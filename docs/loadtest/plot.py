# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "matplotlib",
# ]
# ///

import csv
import matplotlib.pyplot as plt

rows = list(csv.DictReader(open("docs/loadtest/results.csv")))
rps  = [float(r["target_rps"]) for r in rows]

# --- Breaking-point graph ---
plt.figure()
plt.plot(rps, [float(r["error_pct"]) for r in rows], marker="o", color="crimson")
plt.xlabel("Offered RPS")
plt.ylabel("Error rate (%)")
plt.title("Breaking point")
plt.axhline(y=0, color="gray", linestyle="--", alpha=0.5)
for r in rows:
    if float(r["error_pct"]) > 0:
        bp_rps = float(r["target_rps"])
        plt.axvline(x=bp_rps, color="orange", linestyle=":", label=f"Break at {bp_rps} RPS")
        plt.annotate(f"{bp_rps}", xy=(bp_rps, plt.ylim()[1]*0.9),
                     xytext=(10, -10), textcoords="offset points", color="orange")
        break
plt.legend()
plt.savefig("docs/loadtest/breaking-point.png", dpi=150, bbox_inches="tight")
print("Saved breaking-point.png")

# --- Latency graph ---
plt.figure()
for k, lbl in [("p50_ms", "p50"), ("p95_ms", "p95"), ("p99_ms", "p99")]:
    plt.plot(rps, [float(r[k]) for r in rows], marker="o", label=lbl)
plt.xlabel("Offered RPS")
plt.ylabel("Latency (ms)")
plt.title("RPS vs latency")
plt.legend()
plt.savefig("docs/loadtest/latency.png", dpi=150, bbox_inches="tight")
print("Saved latency.png")
