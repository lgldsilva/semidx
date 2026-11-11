"""Metrics identical to internal/eval/metrics.go (graded nDCG, binary MRR/P/R)."""
import json
import math


def grades(q):
    return {r["file"]: r["grade"] for r in q["relevant"]}


def ndcg(ranked, g, k=10):
    if k <= 0 or not g:
        return 0.0
    k = min(k, len(ranked))
    dcg, seen = 0.0, set()
    for i in range(k):
        f = ranked[i]
        if f in seen:
            continue
        if g.get(f, 0) > 0:
            dcg += (2 ** g[f] - 1) / math.log2(i + 2)
            seen.add(f)
    ideal = sorted(g.values(), reverse=True)[:k]
    idcg = sum((2 ** v - 1) / math.log2(i + 2) for i, v in enumerate(ideal))
    return dcg / idcg if idcg else 0.0


def mrr(ranked, g):
    for i, f in enumerate(ranked):
        if g.get(f, 0) > 0:
            return 1 / (i + 1)
    return 0.0


def recall(ranked, g, k=10):
    if not g:
        return 0.0
    hit = {f for f in ranked[:k] if g.get(f, 0) > 0}
    return len(hit) / len(g)


def score(q, ranked, k=10):
    g = grades(q)
    return {"ndcg": ndcg(ranked, g, k), "mrr": mrr(ranked, g), "recall": recall(ranked, g, k),
            "top1": 1.0 if ranked and g.get(ranked[0], 0) >= 3 else 0.0}


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)["queries"]
