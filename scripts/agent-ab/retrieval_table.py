"""Print the retrieval comparison: ripgrep baseline vs semidx bench artifacts."""
import json
import os
import statistics as st
import sys

MODES = ["keyword", "vector", "hybrid", "hybrid-graph"]


def _load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def main(work):
    rg = _load(os.path.join(work, "rg.json"))
    print("| system | nDCG@10 | MRR | Recall@10 | p50 ms |")
    print("|---|---|---|---|---|")
    print(f"| ripgrep (no index) | {st.mean(r['ndcg'] for r in rg):.3f} | {st.mean(r['mrr'] for r in rg):.3f} | "
          f"{st.mean(r['recall'] for r in rg):.3f} | - |")
    for m in MODES:
        path = os.path.join(work, f"semidx-{m}.json")
        if not os.path.exists(path):
            continue
        r = _load(path)
        print(f"| semidx {m} | {r['ndcg_at_10']:.3f} | {r['mrr']:.3f} | {r['recall_at_10']:.3f} | "
              f"{r['latency_p50_ms']:.0f} |")


if __name__ == "__main__":
    main(sys.argv[1])
