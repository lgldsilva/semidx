"""Task-level A/B: does an agent find the right files faster with semidx?

Conditions:
  grep         Claude Code built-in Read/Grep/Glob only.
  semidx       same tools + semidx MCP (standalone local index); agent chooses.
  semidx-only  Read + semidx MCP (no Grep/Glob): semidx is the only search.
Each gold query runs once per condition, headless (`claude -p`),
isolated from user hooks/plugins/MCP (--setting-sources project,
--strict-mcp-config). Output: one JSON line per run with the answer, metrics,
turns, tokens, cost and wall time.
"""
import concurrent.futures as cf
import json
import os
import subprocess
import sys
import time

from metrics import load, score

# Paths come from run.sh: AB_WORK (index, binary, isolated HOME) and AB_ROOT
# (the repository checkout the agents explore).
E = os.environ["AB_WORK"]
ROOT = os.environ["AB_ROOT"]
MODEL = os.environ.get("AB_MODEL", "sonnet")
OLLAMA = os.environ.get("AB_OLLAMA_URL", "http://localhost:11434")

PROMPT = """You are exploring an unfamiliar code repository (the current directory).
Task: find the files that implement or best answer this: "{query}"
Investigate as you normally would. When done, reply with ONLY a JSON object, no prose:
{{"files": ["repo/relative/path", ...]}} with up to 5 files, most relevant first."""

SEMIDX_HINT = ("A semantic code search MCP tool (semidx: semantic_search, plus code-intel tools) "
               "indexing this repository is available; use it when it helps.")

BASE_TOOLS = ["Read", "Grep", "Glob"]


def mcp_config():
    path = os.path.join(E, "mcp.json")
    env = {"HOME": os.path.join(E, "home"), "SEMIDX_LOCAL_INDEX": os.path.join(E, "idx.db"),
           "SEMIDX_OLLAMA_URL": OLLAMA, "OLLAMA_URL": OLLAMA}
    with open(path, "w", encoding="utf-8") as fh:
        json.dump({"mcpServers": {"semidx": {"command": os.path.join(E, "semidx"), "args": ["mcp"], "env": env}}}, fh)
    return path


def build_cmd(query, cond, mcp_path):
    """claude -p argv for one run under the given condition."""
    tools = ["Read"] if cond == "semidx-only" else BASE_TOOLS
    cmd = ["claude", "-p", PROMPT.format(query=query), "--model", MODEL, "--output-format", "stream-json", "--verbose",
           "--setting-sources", "project", "--strict-mcp-config", "--no-session-persistence",
           "--tools", ",".join(tools)]
    allowed = list(tools)
    if cond in ("semidx", "semidx-only"):
        cmd += ["--mcp-config", mcp_path, "--append-system-prompt", SEMIDX_HINT]
        allowed.append("mcp__semidx")
    else:
        cmd += ["--mcp-config", '{"mcpServers":{}}']
    return cmd + ["--allowedTools", *allowed]


def parse_stream(stdout):
    """Return (final result event or None, tool name -> call count)."""
    out, calls = None, {}
    for line in stdout.splitlines():
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("type") == "assistant":
            for c in ev["message"].get("content", []):
                if c.get("type") == "tool_use":
                    calls[c["name"]] = calls.get(c["name"], 0) + 1
        elif ev.get("type") == "result":
            out = ev
    return out, calls


def parse_files(text):
    """Extract up to 5 paths from the agent's {"files": [...]} answer."""
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end < start or '"files"' not in text[start:end]:
        return []
    try:
        return [f.removeprefix("./") for f in json.loads(text[start:end + 1])["files"]][:5]
    except (json.JSONDecodeError, KeyError, TypeError, AttributeError):
        return []


def run(q, cond, mcp_path):
    t0 = time.time()
    p = subprocess.run(build_cmd(q["query"], cond, mcp_path), cwd=ROOT, capture_output=True, text=True, timeout=900)
    rec = {"id": q["id"], "intent": q["intent"], "cond": cond, "wall_s": round(time.time() - t0, 1), "rc": p.returncode}
    out, calls = parse_stream(p.stdout)
    if out is None:
        rec["error"] = (p.stdout + p.stderr)[-500:]
        return rec
    u = out.get("usage", {})
    rec.update(turns=out.get("num_turns"), cost=out.get("total_cost_usd"), calls=calls,
               in_tok=u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0),
               out_tok=u.get("output_tokens", 0), result=(out.get("result") or "")[-800:])
    rec["files"] = parse_files(rec["result"])
    rec.update(score(q, rec["files"], k=5))
    return rec


def main(dataset, out_path, workers=4):
    qs = load(dataset)
    mcp_path = mcp_config()
    done = set()
    if os.path.exists(out_path):
        with open(out_path, encoding="utf-8") as fh:
            for line in fh:
                r = json.loads(line)
                if "error" not in r:
                    done.add((r["id"], r["cond"]))
    jobs = [(q, c) for q in qs for c in ("grep", "semidx", "semidx-only") if (q["id"], c) not in done]
    with cf.ThreadPoolExecutor(int(workers)) as ex, open(out_path, "a", encoding="utf-8") as fh:
        futs = {ex.submit(run, q, c, mcp_path): (q["id"], c) for q, c in jobs}
        for f in cf.as_completed(futs):
            try:
                rec = f.result()
            except Exception as e:  # keep going; record the failure
                rec = {"id": futs[f][0], "cond": futs[f][1], "error": repr(e)}
            fh.write(json.dumps(rec) + "\n")
            fh.flush()
            print(rec["id"], rec["cond"], rec.get("ndcg"), rec.get("turns"), rec.get("error", "")[:80], flush=True)


if __name__ == "__main__":
    main(*sys.argv[1:])
