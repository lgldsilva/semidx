"""Summarise an agent-ab run: per-condition and per-intent tables, paired diff."""
import collections
import json
import statistics as st
import sys

CONDS = ["grep", "semidx", "semidx-only"]


def main(path):
    with open(path, encoding="utf-8") as fh:
        rows = [json.loads(line) for line in fh]
    ok = [r for r in rows if "error" not in r]
    print(f"runs {len(rows)}  errors {len(rows) - len(ok)}\n")
    print("| condition | n | nDCG@5 | MRR | R@5 | top-1 | turns | input tok | cost USD | wall s | used semidx |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for c in CONDS:
        x = [r for r in ok if r["cond"] == c]
        if not x:
            continue
        used = sum(any(k.startswith("mcp__semidx") for k in r.get("calls", {})) for r in x)

        def m(k, x=x):
            return st.mean(r[k] for r in x)
        print(f"| {c} | {len(x)} | {m('ndcg'):.3f} | {m('mrr'):.3f} | {m('recall'):.3f} | {m('top1'):.2f} | "
              f"{m('turns'):.1f} | {m('in_tok'):.0f} | {sum(r['cost'] for r in x):.2f} | {m('wall_s'):.1f} | {used}/{len(x)} |")
    intents = sorted({r["intent"] for r in ok})
    print("\nnDCG@5 by intent\n\n| condition | " + " | ".join(intents) + " |")
    print("|---|" + "---|" * len(intents))
    for c in CONDS:
        vals = [st.mean([r["ndcg"] for r in ok if r["cond"] == c and r["intent"] == i] or [0]) for i in intents]
        print(f"| {c} | " + " | ".join(f"{v:.3f}" for v in vals) + " |")
    by = collections.defaultdict(dict)
    for r in ok:
        by[r["id"]][r["cond"]] = r
    print()
    for a in ("semidx", "semidx-only"):
        d = [by[i][a]["ndcg"] - by[i]["grep"]["ndcg"] for i in by if a in by[i] and "grep" in by[i]]
        if d:
            w, lo = sum(x > 0.01 for x in d), sum(x < -0.01 for x in d)
            print(f"{a} vs grep: mean nDCG@5 diff {st.mean(d):+.3f}, wins {w}, losses {lo}, ties {len(d) - w - lo}")


if __name__ == "__main__":
    main(sys.argv[1])
