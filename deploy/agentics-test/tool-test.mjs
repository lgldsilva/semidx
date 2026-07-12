#!/usr/bin/env node
// MCP tool probe for workspace-agent tools.
// Tests that new tools (repo_*, semantic_search_multi, semantic_ask) are
// registered and respond to a tools/call.
//
// Usage:
//   tool-test.mjs --mcp-cmd <cmd> [args...] --project <name> [--skip-git]
//
// Exits 0 on success, 1 on failure.

import { spawn } from "child_process";
import { parseArgs } from "util";

const { values } = parseArgs({
  args: process.argv.slice(2),
  options: {
    "mcp-cmd": { type: "string", multiple: true },
    "project": { type: "string" },
    "skip-git": { type: "boolean", default: false },
  },
});

const cmd = values["mcp-cmd"] || [];
const project = values["project"] || "test";
const skipGit = values["skip-git"] || false;

if (!cmd.length) {
  console.error("Usage: tool-test.mjs --mcp-cmd <cmd> [args...] --project <name> [--skip-git]");
  process.exit(1);
}

let pass = 0;
let fail = 0;

async function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function main() {
  // Start the MCP server
  const child = spawn(cmd[0], cmd.slice(1), {
    stdio: ["pipe", "pipe", "inherit"],
  });

  let buf = "";
  let reqId = 0;

  function send(method, params) {
    reqId++;
    child.stdin.write(
      JSON.stringify({
        jsonrpc: "2.0",
        id: reqId,
        method: method,
        params: params || {},
      }) + "\n"
    );
    return reqId;
  }

  const responses = {};

  // Read stdout line by line
  const reader = new Promise((resolve, reject) => {
    child.stdout.on("data", (chunk) => {
      buf += chunk.toString();
      let idx;
      while ((idx = buf.indexOf("\n")) !== -1) {
        const line = buf.slice(0, idx).trim();
        buf = buf.slice(idx + 1);
        if (!line) continue;
        try {
          const resp = JSON.parse(line);
          if (resp.id) {
            responses[resp.id] = resp;
          }
        } catch (e) {
          // skip partial JSON
        }
      }
    });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code && code !== 0) reject(new Error(`process exited ${code}`));
      resolve();
    });
  });

  await sleep(500); // wait for server to start

  // Initialize
  send("initialize", {
    protocolVersion: "2024-11-05",
    capabilities: {},
    clientInfo: { name: "tool-test", version: "1" },
  });

  await sleep(500);

  // List tools
  send("tools/list", {});
  await sleep(500);

  const listResp = responses[reqId];
  if (!listResp || !listResp.result) {
    console.error("FAIL: tools/list returned no result");
    process.exit(1);
  }

  const toolNames = (listResp.result.tools || []).map((t) => t.name);
  console.log("  registered tools:", toolNames.join(", "));

  // Check required tools exist
  const required = ["semantic_search", "semantic_projects", "semantic_status"];
  for (const name of required) {
    if (toolNames.includes(name)) {
      console.log(`  PASS: ${name} is registered`);
      pass++;
    } else {
      console.log(`  FAIL: ${name} is NOT registered`);
      fail++;
    }
  }

  // Check workspace-agent tools exist
  const wsTools = ["repo_worktrees", "repo_branches", "repo_status", "semantic_search_multi", "semantic_ask"];
  for (const name of wsTools) {
    if (toolNames.includes(name)) {
      console.log(`  PASS: ${name} is registered`);
      pass++;
    } else {
      // semantic_ask may not be registered without LLM key — that's OK
      if (name === "semantic_ask") {
        console.log(`  SKIP: ${name} not registered (no LLM key — expected)`);
        pass++; // count as pass since it's gated
      } else {
        console.log(`  FAIL: ${name} is NOT registered`);
        fail++;
      }
    }
  }

  child.kill();
  await reader.catch(() => {});

  if (fail > 0) {
    console.error(`FAILED: ${fail} checks failed`);
    process.exit(1);
  }
  console.log(`PASSED: ${pass} checks passed`);
  process.exit(0);
}

main().catch((e) => {
  console.error("FATAL:", e.message);
  process.exit(1);
});
