"use strict";

import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const extensionPath = path.join(repositoryRoot, "runtime", "scriptboard-extension.ts");
const temporaryRoot = await mkdtemp(path.join(tmpdir(), "scriptboard-runtime-contract-"));

try {
  let source = await readFile(extensionPath, "utf8");
  source = source
    .replace(
      'import { Type } from "@earendil-works/pi-ai";',
      "const Type = new Proxy({}, { get: () => (...args) => ({ args }) });",
    )
    .replace('import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";\n', "")
    .replace('import { createConnection } from "node:net";', "const createConnection = globalThis.__scriptboardCreateConnection;");

  const executableExtension = path.join(temporaryRoot, "scriptboard-extension.ts");
  await writeFile(executableExtension, source, "utf8");

  const requests = [];
  globalThis.__scriptboardCreateConnection = () => {
    const socket = new EventEmitter();
    socket.setEncoding = () => socket;
    socket.destroy = () => {};
    socket.write = payload => {
      const request = JSON.parse(payload);
      requests.push({ toolCallId: request.toolCallId, decision: request.decision });
      const response = request.decision
        ? { status: "success", content: { ok: true } }
        : { status: "approval_required", approval: { id: `approval-${request.toolCallId}`, title: "Approve", message: "Bound action" } };
      queueMicrotask(() => socket.emit("data", `${JSON.stringify(response)}\n`));
    };
    queueMicrotask(() => socket.emit("connect"));
    return socket;
  };
  process.env.SCRIPTBOARD_BROKER_ENDPOINT = "fixture";
  process.env.SCRIPTBOARD_BROKER_CAPABILITY = "fixture";

  const registered = new Map();
  const extension = await import(`${pathToFileURL(executableExtension)}?contract=${Date.now()}`);
  extension.default({ registerTool: definition => registered.set(definition.name, definition) });
  const tool = registered.get("perform_ui_action");
  assert.ok(tool, "perform_ui_action was not registered");

  const context = { ui: { confirm: () => new Promise(resolve => setTimeout(() => resolve(true), 20)) } };
  const first = tool.execute("parallel-1", { action: "ai.test_llm" }, undefined, undefined, context);
  const second = tool.execute("parallel-2", { action: "ai.save_defaults" }, undefined, undefined, context);
  await Promise.all([first, second]);

  assert.deepEqual(requests, [
    { toolCallId: "parallel-1", decision: "" },
    { toolCallId: "parallel-1", decision: "approve" },
    { toolCallId: "parallel-2", decision: "" },
    { toolCallId: "parallel-2", decision: "approve" },
  ], "state-changing tool calls must finish their approval round trip one at a time");
} finally {
  delete globalThis.__scriptboardCreateConnection;
  await rm(temporaryRoot, { recursive: true, force: true });
}

console.log("Assistant runtime contract passed.");
