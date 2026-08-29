// pib extension for pi.
//
// Registers the `pib` tool, which asks the running pib TUI to spawn an agent
// and blocks until that agent stops, and the `pib_done` / `pib_ask` tools a
// spawned agent uses to say why it stopped. pi cannot tell "task complete"
// from "waiting on a question" — both end a turn with stopReason "stop" — so
// the distinction is declared explicitly by the child.
//
// Dependency-free on purpose: pi loads .ts through jiti at runtime, and this
// file is embedded in the pib binary, so it must not need a build step.

import net from "node:net";
import { readFileSync, writeFileSync } from "node:fs";

const SOCKET_ENV = "PIB_SOCKET";
const EXIT_FILE_ENV = "PIB_EXIT_FILE";
const AGENT_ENV = "PIB_AGENT";

const POINTER_FILE = ".pib/socket";
const DEFAULT_SOCKET = ".pib/pib.sock";

function socketPath(): string {
  const fromEnv = process.env[SOCKET_ENV];
  if (fromEnv) return fromEnv;

  // pib records the socket it actually bound. It moves out of the workspace
  // when the repository sits deep enough that the full path would exceed the
  // kernel's limit, so the conventional location is only a last resort.
  try {
    const pointed = readFileSync(POINTER_FILE, "utf8").trim();
    if (pointed) return pointed;
  } catch {
    // No pointer file; fall through.
  }

  return DEFAULT_SOCKET;
}

function text(body: string) {
  return { content: [{ type: "text", text: body }], details: {} };
}

/**
 * Send one request to pib and wait for the agent it starts to stop. The
 * connection stays open for the whole run, so there is nothing to correlate:
 * the reply on this socket is the answer to this request.
 */
function ask(payload: Record<string, unknown>, signal?: AbortSignal): Promise<any> {
  const path = socketPath();

  return new Promise((resolve, reject) => {
    const socket = net.createConnection(path);
    let buffer = "";
    let settled = false;

    const finish = (fn: () => void) => {
      if (settled) return;
      settled = true;
      fn();
    };

    const onAbort = () => {
      // Closing the socket tells pib to kill the agent's window.
      socket.destroy();
      finish(() => reject(new Error("Cancelled.")));
    };

    if (signal) {
      if (signal.aborted) return onAbort();
      signal.addEventListener("abort", onAbort, { once: true });
    }

    socket.on("connect", () => socket.write(JSON.stringify(payload)));

    socket.on("data", (chunk) => {
      buffer += chunk.toString();
      const newline = buffer.indexOf("\n");
      if (newline === -1) return;
      try {
        const response = JSON.parse(buffer.slice(0, newline));
        finish(() => resolve(response));
      } catch (error: any) {
        finish(() => reject(new Error(`pib sent an unreadable reply: ${error.message}`)));
      }
      socket.end();
    });

    socket.on("error", (error: any) => {
      const hint =
        error.code === "ENOENT" || error.code === "ECONNREFUSED"
          ? `pib is not running (no listener at ${path}). Start pib in this repository and try again.`
          : `Could not reach pib at ${path}: ${error.message}`;
      finish(() => reject(new Error(hint)));
    });

    socket.on("close", () => {
      finish(() => reject(new Error("pib closed the connection before the agent finished.")));
    });
  });
}

/** Report elapsed time while the caller is blocked. */
function ticker(label: string, onUpdate?: (partial: any) => void) {
  if (!onUpdate) return () => {};

  const started = Date.now();
  const timer = setInterval(() => {
    const seconds = Math.round((Date.now() - started) / 1000);
    const elapsed = seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m${seconds % 60}s`;
    try {
      onUpdate(text(`${label} running (${elapsed})…`));
    } catch {
      // Progress display is best effort; never fail the tool over it.
    }
  }, 5000);

  return () => clearInterval(timer);
}

export default function pibExtension(pi: any) {
  const exitFile = process.env[EXIT_FILE_ENV];

  // ── caller side ──

  pi.registerTool({
    name: "pib",
    label: "pib",
    description:
      "Run another agent and wait for its answer. " +
      "This is a BLOCKING call: it returns only once the agent has finished, and its answer is the result of this call. " +
      "Do not poll, sleep, tail logs, or read session files to check on it — there is nothing to check, the answer arrives here. " +
      "Pass `agent` and `task` to start one. " +
      "If the result comes back with status \"needs_input\", the agent asked a question: call this tool again with the returned `session` and your `answer` to continue it. " +
      "To run agents concurrently, make several calls in the same turn.",
    promptSnippet:
      "Run another agent with `pib` and wait for its answer. Blocking: the answer is the result of the call. " +
      "Status \"needs_input\" means the agent asked a question — call again with its `session` and your `answer`.",
    parameters: {
      type: "object",
      properties: {
        agent: {
          type: "string",
          description: "Agent definition to run, e.g. \"scout\" or \"researcher\".",
        },
        name: {
          type: "string",
          description: "Display name for the agent's window. Defaults to the agent name.",
        },
        task: {
          type: "string",
          description: "What the agent should do. Include the context it needs; it does not see this conversation.",
        },
        issue: {
          type: "number",
          description: "Issue the agent is working on, so pib shows that issue as in progress while it runs.",
        },
        session: {
          type: "string",
          description: "Session to continue, from a previous needs_input result.",
        },
        answer: {
          type: "string",
          description: "Reply to the question a needs_input result asked.",
        },
      },
    },

    async execute(_toolCallId: string, params: any, signal: AbortSignal, onUpdate: any) {
      const resuming = Boolean(params?.session);

      if (!resuming && !params?.agent) {
        throw new Error("Pass `agent` and `task` to start an agent, or `session` and `answer` to continue one.");
      }

      const label = params?.name || params?.agent || "agent";
      const stop = ticker(label, onUpdate);

      try {
        const response = await ask(
          resuming
            ? { op: "resume", session: params.session, answer: params.answer ?? "" }
            : {
                op: "spawn",
                agent: params.agent,
                name: params.name ?? "",
                task: params.task ?? "",
                issue: params.issue ?? 0,
                caller: process.env[AGENT_ENV] ?? "",
              },
          signal,
        );

        if (response.error) throw new Error(response.error);

        const status = response.status ?? "unknown";
        const body = response.text?.trim() || "(the agent produced no output)";
        const session = response.session ? `\n\nsession: ${response.session}` : "";

        switch (status) {
          case "done":
            return text(body);
          case "needs_input":
            return text(`The agent needs an answer before it can continue:\n\n${body}${session}`);
          case "error":
            return text(`The agent failed:\n\n${body}${session}`);
          default:
            return text(
              `The agent stopped without reporting a result — it was probably closed by hand. ` +
                `Its last message was:\n\n${body}${session}`,
            );
        }
      } finally {
        stop();
      }
    },
  });

  // ── agent side ──
  //
  // Only registered inside an agent pib started, since there is nothing to
  // report back to otherwise.
  if (!exitFile) return;

  pi.registerTool({
    name: "pib_done",
    label: "Done",
    description:
      "Call this when your task is complete. It ends your session and returns your work to whoever asked for it. " +
      "Your last message before calling this is what they receive, so say what you found before calling it.",
    promptSnippet:
      "When your task is complete, call `pib_done`. Your last message before that call is what the caller receives.",
    parameters: { type: "object", properties: {} },

    async execute(_toolCallId: string, _params: any, _signal: AbortSignal, _onUpdate: any, ctx: any) {
      writeFileSync(exitFile, JSON.stringify({ type: "done" }));
      ctx.shutdown();
      return text("Done. Returning to the caller.");
    },
  });

  pi.registerTool({
    name: "pib_ask",
    label: "Ask",
    description:
      "Call this when you cannot continue without an answer — a decision only the caller can make, missing context, or a blocked task. " +
      "It ends your session and puts your question to the caller, who can answer and resume you. " +
      "Prefer finishing with what you have; only ask when the answer changes what you would do.",
    promptSnippet:
      "If you cannot continue without an answer, call `pib_ask` with your question. The caller can answer and resume you.",
    parameters: {
      type: "object",
      properties: {
        question: {
          type: "string",
          description: "What you need to know, and why it blocks you.",
        },
      },
      required: ["question"],
    },

    async execute(_toolCallId: string, params: any, _signal: AbortSignal, _onUpdate: any, ctx: any) {
      writeFileSync(exitFile, JSON.stringify({ type: "ask", message: params?.question ?? "" }));
      ctx.shutdown();
      return text("Question sent. Your session will pause until the caller answers.");
    },
  });
}
