"""Naive ripgrep ranker: what a single-shot grep gives without any index.

Per query: split into terms (camelCase split too), drop stopwords, count
case-insensitive matches per file with rg (respects .gitignore), score
files by sum(idf * log(1+count)). No iteration, no reading — a floor for
what an agent's first grep sees.
"""
import collections
import json
import math
import re
import subprocess
import sys

from metrics import load, score

STOP = set("a an the and or of to in on by for from with into through as at is are be when how where what which "
           "using use based before after than instead their its this that two no any via each per".split())


def terms(q):
    words = re.findall(r"[A-Za-z][A-Za-z0-9_]*", q)
    out = []
    for w in words:
        parts = re.findall(r"[A-Z]?[a-z0-9]+|[A-Z]+(?![a-z])", w) or [w]
        for p in ([w] if len(parts) > 1 else []) + parts:
            p = p.lower()
            if len(p) >= 3 and p not in STOP and p not in out:
                out.append(p)
    return out


def counts(root, term):
    r = subprocess.run(["rg", "-i", "-c", "--no-messages", "-F", term, "."], cwd=root,
                       capture_output=True, text=True)
    res = {}
    for line in r.stdout.splitlines():
        path, _, n = line.rpartition(":")
        res[path.removeprefix("./")] = int(n)
    return res


def main(dataset, root, out):
    total_files = len(subprocess.run(["rg", "--files", "."], cwd=root, capture_output=True, text=True).stdout.split())
    rows = []
    for q in load(dataset):
        sc = collections.Counter()
        for t in terms(q["query"]):
            c = counts(root, t)
            if not c:
                continue
            idf = math.log(total_files / len(c))
            for f, n in c.items():
                sc[f] += idf * math.log1p(n)
        ranked = [f for f, _ in sc.most_common(10)]
        rows.append({"id": q["id"], "intent": q["intent"], "ranked": ranked, **score(q, ranked)})
    with open(out, "w", encoding="utf-8") as fh:
        json.dump(rows, fh, indent=1)


if __name__ == "__main__":
    main(*sys.argv[1:4])
