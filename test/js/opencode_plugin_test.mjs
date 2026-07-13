import assert from "node:assert/strict"
import { createServer } from "node:http"
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  stat,
  writeFile,
} from "node:fs/promises"
import { tmpdir } from "node:os"
import { basename, dirname, join } from "node:path"
import { pathToFileURL } from "node:url"

const pluginPath = process.argv[2]
if (!pluginPath) throw new Error("usage: node opencode_plugin_test.mjs PLUGIN.mjs")

const source = await readFile(pluginPath, "utf8")
let imported
let importError

async function pluginModule() {
  if (imported) return imported
  if (importError) throw importError
  try {
    imported = await import(`${pathToFileURL(pluginPath).href}?contract=1`)
    assert.equal(typeof imported.WispDeck, "function", "template must export WispDeck")
    assert.deepEqual(Object.keys(imported).sort(), ["WispDeck"], "template must not expose test-only plugin exports")
    return imported
  } catch (error) {
    importError = error
    throw error
  }
}

const tests = []
function test(name, fn) {
  tests.push({ name, fn })
}

async function waitFor(predicate, message, timeoutMs = 2000) {
  const deadline = Date.now() + timeoutMs
  let lastError
  while (Date.now() < deadline) {
    try {
      const value = await predicate()
      if (value) return value
    } catch (error) {
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  if (lastError) throw lastError
  throw new Error(`timed out waiting for ${message}`)
}

function cleanAttentionEnv(extra = {}) {
  const env = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith("WISP_DECK_ATTENTION_") || key.startsWith("OPENCODE_SERVER_")) {
      delete env[key]
    }
  }
  return { ...env, ...extra }
}

async function withProcessEnv(env, fn) {
  const saved = process.env
  process.env = env
  try {
    return await fn()
  } finally {
    process.env = saved
  }
}

async function makeRuntime(options = {}) {
  const base = await mkdtemp(join(tmpdir(), "wisp-opencode-test-"))
  const generation = options.generation || "generation-test"
  const parent = join(base, "generation")
  const stateFile = join(parent, "state")
  if (options.parent !== false) await mkdir(parent, { mode: 0o700 })
  if (options.initial !== false && options.parent !== false) {
    const initial = options.initial || `1\t${generation}\t0\tunknown\t-\n`
    await writeFile(stateFile, initial, { mode: options.initialMode || 0o600 })
  }

  const env = cleanAttentionEnv({
    WISP_DECK_ATTENTION_FILE: stateFile,
    WISP_DECK_ATTENTION_GENERATION: generation,
    ...(options.env || {}),
  })
  const mod = await pluginModule()
  const hooks = await withProcessEnv(env, () => mod.WispDeck({
    directory: options.directory || "/project",
    serverUrl: options.serverUrl,
  }))

  return {
    base,
    env,
    generation,
    hooks,
    parent,
    stateFile,
    async cleanup() {
      await rm(base, { force: true, recursive: true })
    },
  }
}

async function send(runtime, event) {
  assert.equal(typeof runtime.hooks?.event, "function", "active plugin must expose only an event hook")
  await runtime.hooks.event({ event })
}

async function record(runtime) {
  return readFile(runtime.stateFile, "utf8")
}

function event(type, properties) {
  return properties === undefined ? { id: `event-${type}`, type } : { id: `event-${type}`, type, properties }
}

function session(id, parentID) {
  const value = {
    id,
    projectID: "project",
    directory: "/project",
    time: { created: 1, updated: 1 },
  }
  if (parentID !== undefined) value.parentID = parentID
  return value
}

function created(id, parentID) {
  const info = session(id, parentID)
  return event("session.created", { sessionID: id, info })
}

function updated(id, parentID) {
  const info = session(id, parentID)
  return event("session.updated", { sessionID: id, info })
}

function deleted(id, parentID) {
  const info = session(id, parentID)
  return event("session.deleted", { sessionID: id, info })
}

function status(id, type) {
  const value = type === "retry"
    ? { type, attempt: 1, message: "retrying", next: Date.now() + 1000 }
    : { type }
  return event("session.status", { sessionID: id, status: value })
}

function questionAsked(id, sessionID) {
  return event("question.asked", {
    id,
    sessionID,
    questions: [{ header: "Choice", question: "Continue?", options: [] }],
  })
}

function questionReplied(id, sessionID) {
  return event("question.replied", { requestID: id, sessionID, answers: [[]] })
}

function questionRejected(id, sessionID) {
  return event("question.rejected", { requestID: id, sessionID })
}

function permissionAsked(id, sessionID) {
  return event("permission.asked", {
    id,
    sessionID,
    permission: "bash",
    patterns: ["*"],
    metadata: {},
    always: [],
  })
}

function permissionReplied(id, sessionID) {
  return event("permission.replied", { requestID: id, sessionID, reply: "once" })
}

function apiError(message = "request failed") {
  return { name: "APIError", data: { message, isRetryable: false } }
}

function unknownError(message = "unknown failure") {
  return { name: "UnknownError", data: { message } }
}

function sessionError(id, sessionID, error = apiError()) {
  return { id, type: "session.error", properties: { sessionID, error } }
}

async function establishReady(runtime, root = "root") {
  await send(runtime, created(root))
  await send(runtime, status(root, "idle"))
}

test("template is event-only plain JavaScript with no obsolete effects", async () => {
  const forbidden = [
    [/child_process/, "child process import"],
    [/\bspawn\s*\(/, "spawn"],
    [/\bexecSync\s*\(/, "execSync"],
    [/afplay|osascript/i, "direct notification"],
    [/process\.stdout|\\x1b\]|\\u001b\]/, "terminal title output"],
    [/spinner/i, "spinner"],
    [/(^|[^.A-Za-z0-9_$])setTimeout\s*\(|\bsetInterval\s*\(/m, "semantic timer"],
    [/tool\.execute\.(?:after|before)/, "tool hook"],
    [/session\.idle/, "deprecated idle event"],
    [/server\.connected/, "undelivered retry event"],
    [/@opencode-ai\/plugin/, "event-union/type import"],
  ]
  for (const [pattern, label] of forbidden) {
    assert.doesNotMatch(source, pattern, `template contains forbidden ${label}`)
  }
  assert.doesNotMatch(source, /:\s*(?:string|number|boolean|any|ReturnType|void)\b/, "template contains TypeScript-only annotations")
  assert.match(source, /request\.setTimeout\(HTTP_TIMEOUT_MS,/, "HTTP requests need an isolated transport deadline")
  assert.match(source, /globalThis\.setTimeout\(/, "HTTP requests need an absolute lifetime deadline")
  await pluginModule()
})

test("plugin is inert when either Wisp variable is absent", async () => {
  const mod = await pluginModule()
  const cases = [
    {},
    { WISP_DECK_ATTENTION_FILE: "/should/not/exist" },
    { WISP_DECK_ATTENTION_GENERATION: "generation-only" },
  ]
  for (const extra of cases) {
    const hooks = await withProcessEnv(cleanAttentionEnv(extra), () => mod.WispDeck({ directory: "/project" }))
    assert.deepEqual(hooks, {}, `plugin was not inert for env ${JSON.stringify(extra)}`)
  }
})

test("publisher writes exact atomic TSV, mode 0600, and resumes matching sequence", async () => {
  const runtime = await makeRuntime({
    initial: "1\tgeneration-test\t7\tready\t-\n",
    initialMode: 0o644,
  })
  try {
    await send(runtime, created("root"))
    await send(runtime, status("root", "busy"))
    assert.equal(await record(runtime), "1\tgeneration-test\t8\tworking\t-\n")
    assert.equal((await stat(runtime.stateFile)).mode & 0o777, 0o600)
    assert.deepEqual(await readdir(runtime.parent), [basename(runtime.stateFile)], "atomic sibling temp leaked")
  } finally {
    await runtime.cleanup()
  }
})

test("publisher rejects oversized recovery, stays bounded, and resets mismatched generation", async () => {
  for (const initial of [
    `1\tgeneration-test\t${"9".repeat(5000)}\tready\t-\n`,
    "1\tanother-generation\t41\tready\t-\n",
  ]) {
    const runtime = await makeRuntime({ initial })
    try {
      await send(runtime, created("root"))
      await send(runtime, status("root", "busy"))
      assert.equal(await record(runtime), "1\tgeneration-test\t1\tworking\t-\n")
      assert.ok((await stat(runtime.stateFile)).size < 4096)
    } finally {
      await runtime.cleanup()
    }
  }
})

test("removed generation parent permanently fences a fail-silent writer", async () => {
  const runtime = await makeRuntime()
  try {
    await send(runtime, created("root"))
    await rm(runtime.parent, { force: true, recursive: true })
    await send(runtime, status("root", "busy"))
    await assert.rejects(stat(runtime.parent), { code: "ENOENT" })
    await mkdir(runtime.parent, { mode: 0o700 })
    await send(runtime, status("root", "idle"))
    await assert.rejects(stat(runtime.stateFile), { code: "ENOENT" })
  } finally {
    await runtime.cleanup()
  }
})

test("recreated generation parent is fenced before the first following event", async () => {
  const runtime = await makeRuntime()
  try {
    const original = await stat(runtime.parent)
    let replacement
    for (let attempt = 0; attempt < 8; attempt += 1) {
      await rm(runtime.parent, { force: true, recursive: true })
      await mkdir(runtime.parent, { mode: 0o700 })
      replacement = await stat(runtime.parent)
      if (replacement.dev !== original.dev || replacement.ino !== original.ino) break
    }
    assert.ok(replacement.dev !== original.dev || replacement.ino !== original.ino, "test could not allocate a distinct parent inode")
    await send(runtime, created("root"))
    await send(runtime, status("root", "busy"))
    await assert.rejects(stat(runtime.stateFile), { code: "ENOENT" })
    await send(runtime, status("root", "idle"))
    await assert.rejects(stat(runtime.stateFile), { code: "ENOENT" })
    assert.deepEqual(await readdir(runtime.parent), [])
  } finally {
    await runtime.cleanup()
  }
})

test("publisher deduplicates semantics and assigns a new done identity per armed turn", async () => {
  const runtime = await makeRuntime()
  try {
    await send(runtime, created("root"))
    await send(runtime, status("root", "busy"))
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tworking\t-\n")
    await send(runtime, status("root", "busy"))
    await send(runtime, status("root", "retry"))
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tworking\t-\n")
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tdone\n")
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tdone\n")
    await send(runtime, status("root", "busy"))
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tattention\tdone\n")
  } finally {
    await runtime.cleanup()
  }
})

test("recovered attention attaches its first identity without a duplicate sequence", async () => {
  const runtime = await makeRuntime({
    initial: "1\tgeneration-test\t7\tattention\tquestion\n",
  })
  try {
    await send(runtime, created("root"))
    await send(runtime, questionAsked("q-recovered", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t7\tattention\tquestion\n")
    await send(runtime, questionAsked("q-recovered", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t7\tattention\tquestion\n")
    await send(runtime, questionAsked("q-new", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t8\tattention\tquestion\n")
  } finally {
    await runtime.cleanup()
  }
})

test("root retry arms completion while initial and child idle never complete", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tready\t-\n")
    await send(runtime, created("child", "root"))
    await send(runtime, status("child", "busy"))
    await send(runtime, status("child", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tready\t-\n")
    await send(runtime, status("root", "retry"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tworking\t-\n")
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tdone\n")
  } finally {
    await runtime.cleanup()
  }
})

test("an unresolved child request suppresses a hidden root completion latch", async () => {
  const runtime = await makeRuntime()
  try {
    await send(runtime, created("root"))
    await send(runtime, status("root", "busy"))
    await send(runtime, questionAsked("late-child-question", "late-child"))
    await send(runtime, status("root", "idle"))
    await send(runtime, created("late-child", "root"))
    assert.ok((await record(runtime)).endsWith("\tattention\tquestion\n"))
    await send(runtime, questionReplied("late-child-question", "late-child"))
    assert.ok((await record(runtime)).endsWith("\tready\t-\n"), "clearing the request revealed a hidden done latch")
  } finally {
    await runtime.cleanup()
  }
})

const malformedLiveEvents = [
  {
    name: "question contains a null item",
    make() {
      const value = questionAsked("bad-question-null", "root")
      value.properties.questions = [null]
      return value
    },
  },
  {
    name: "question contains a malformed option",
    make() {
      const value = questionAsked("bad-question-option", "root")
      value.properties.questions[0].options = [{ label: "Yes" }]
      return value
    },
  },
  {
    name: "question tool identity is partial",
    make() {
      const value = questionAsked("bad-question-tool", "root")
      value.properties.tool = { messageID: "message" }
      return value
    },
  },
  {
    name: "question reply answers contain a non-array",
    make() {
      const value = questionReplied("unknown-question", "root")
      value.properties.answers = [null]
      return value
    },
  },
  {
    name: "permission tool identity is partial",
    make() {
      const value = permissionAsked("bad-permission-tool", "root")
      value.properties.tool = { messageID: "message" }
      return value
    },
  },
  {
    name: "retry attempt is negative",
    make() {
      const value = status("root", "retry")
      value.properties.status.attempt = -1
      return value
    },
  },
  {
    name: "retry deadline is nonfinite",
    make() {
      const value = status("root", "retry")
      value.properties.status.next = Number.POSITIVE_INFINITY
      return value
    },
  },
  {
    name: "retry action is partial",
    make() {
      const value = status("root", "retry")
      value.properties.status.action = { reason: "rate-limit" }
      return value
    },
  },
  {
    name: "session error payload is absent",
    make() {
      return { id: "missing-error", type: "session.error", properties: { sessionID: "root" } }
    },
  },
  {
    name: "session error union is malformed",
    make() {
      return sessionError("malformed-error", "root", { name: "APIError" })
    },
  },
]

for (const item of malformedLiveEvents) {
  test(`malformed live ${item.name} stays unknown`, async () => {
    const runtime = await makeRuntime()
    try {
      await establishReady(runtime)
      await send(runtime, item.make())
      assert.equal(await record(runtime), "1\tgeneration-test\t2\tunknown\t-\n")
    } finally {
      await runtime.cleanup()
    }
  })
}

test("oversized identifiers and model collections fail to unknown", async () => {
  const identifierRuntime = await makeRuntime()
  try {
    await establishReady(identifierRuntime)
    await send(identifierRuntime, questionAsked("x".repeat(257), "root"))
    assert.equal(await record(identifierRuntime), "1\tgeneration-test\t2\tunknown\t-\n")
  } finally {
    await identifierRuntime.cleanup()
  }

  const collectionRuntime = await makeRuntime()
  try {
    await establishReady(collectionRuntime)
    for (let index = 0; index < 4097; index += 1) {
      await send(collectionRuntime, created(`child-${index}`, "root"))
    }
    assert.equal(await record(collectionRuntime), "1\tgeneration-test\t2\tunknown\t-\n")
  } finally {
    await collectionRuntime.cleanup()
  }
})

test("error identity collection overflow fails unknown instead of evicting", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, created("child", "root"))
    for (let index = 0; index < 4097; index += 1) {
      await send(runtime, sessionError(`child-error-${index}`, "child", unknownError()))
    }
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tunknown\t-\n")
  } finally {
    await runtime.cleanup()
  }
})

test("child questions alert immediately, dedupe IDs, and replies/rejections clear exactly one", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, created("child", "root"))
    await send(runtime, questionAsked("q-1", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tquestion\n")
    await send(runtime, questionAsked("q-1", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tquestion\n")
    await send(runtime, questionAsked("q-2", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tquestion\n")
    await send(runtime, questionRejected("q-2", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tquestion\n", "revealing an already-alerted request duplicated attention")
    await send(runtime, questionReplied("q-1", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tready\t-\n")
  } finally {
    await runtime.cleanup()
  }
})

test("question precedence preserves independently arriving permissions", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, permissionAsked("p-1", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tpermission\n")
    await send(runtime, questionAsked("q-1", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tquestion\n")
    await send(runtime, permissionAsked("p-2", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tquestion\n")
    await send(runtime, questionReplied("q-1", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tattention\tpermission\n")
    await send(runtime, permissionReplied("p-2", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tattention\tpermission\n", "older permission must remain pending without duplicate alert")
    await send(runtime, permissionReplied("p-1", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t5\tready\t-\n")
  } finally {
    await runtime.cleanup()
  }
})

test("reducer enforces question permission error done working ready precedence", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, status("root", "busy"))
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\tdone\n")
    await send(runtime, status("root", "busy"))
    await send(runtime, sessionError("priority-error", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t5\tattention\terror\n")
    await send(runtime, permissionAsked("priority-permission", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t6\tattention\tpermission\n")
    await send(runtime, questionAsked("priority-question", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t7\tattention\tquestion\n")
    await send(runtime, questionReplied("priority-question", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t8\tattention\tpermission\n")
    await send(runtime, permissionReplied("priority-permission", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t9\tattention\terror\n")
  } finally {
    await runtime.cleanup()
  }
})

test("root error suppresses its following idle duplicate and child error is not terminal", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, created("child", "root"))
    await send(runtime, sessionError("child-error", "child", unknownError()))
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tready\t-\n")
    await send(runtime, status("root", "busy"))
    await send(runtime, sessionError("root-error", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\terror\n")
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\terror\n")
    await send(runtime, status("root", "busy"))
    await send(runtime, status("root", "idle"))
    assert.equal(await record(runtime), "1\tgeneration-test\t5\tattention\tdone\n")
  } finally {
    await runtime.cleanup()
  }
})

test("duplicate session.error uses stable event identity while a new error alerts", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, status("root", "busy"))
    const first = sessionError("error-event-1", "root")
    await send(runtime, first)
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\terror\n")
    await send(runtime, first)
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tattention\terror\n")
    await send(runtime, {
      ...first,
      id: "error-event-2",
    })
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tattention\terror\n")
  } finally {
    await runtime.cleanup()
  }
})

test("unknown-session status and error fail to unknown until identity arrives", async () => {
  const statusRuntime = await makeRuntime()
  try {
    await establishReady(statusRuntime, "known")
    await send(statusRuntime, status("late-root", "busy"))
    assert.equal(await record(statusRuntime), "1\tgeneration-test\t2\tunknown\t-\n")
    await send(statusRuntime, created("late-root"))
    assert.equal(await record(statusRuntime), "1\tgeneration-test\t3\tworking\t-\n")
  } finally {
    await statusRuntime.cleanup()
  }

  const errorRuntime = await makeRuntime()
  try {
    await establishReady(errorRuntime, "known")
    await send(errorRuntime, sessionError("late-error", "late-error-root", unknownError()))
    assert.equal(await record(errorRuntime), "1\tgeneration-test\t2\tunknown\t-\n")
    await send(errorRuntime, created("late-error-root"))
    assert.equal(await record(errorRuntime), "1\tgeneration-test\t3\tattention\terror\n")
  } finally {
    await errorRuntime.cleanup()
  }
})

test("session.updated parent change clears former-root armed and terminal state", async () => {
  const armedRuntime = await makeRuntime()
  try {
    await establishReady(armedRuntime, "parent")
    await send(armedRuntime, created("former-root"))
    await send(armedRuntime, status("former-root", "busy"))
    assert.equal(await record(armedRuntime), "1\tgeneration-test\t3\tworking\t-\n")
    await send(armedRuntime, updated("former-root", "parent"))
    assert.equal(await record(armedRuntime), "1\tgeneration-test\t4\tready\t-\n")
    await send(armedRuntime, status("former-root", "idle"))
    assert.equal(await record(armedRuntime), "1\tgeneration-test\t4\tready\t-\n")
  } finally {
    await armedRuntime.cleanup()
  }

  const terminalRuntime = await makeRuntime()
  try {
    await establishReady(terminalRuntime, "parent")
    await send(terminalRuntime, created("former-root"))
    await send(terminalRuntime, status("former-root", "busy"))
    await send(terminalRuntime, sessionError("former-root-error", "former-root"))
    assert.equal(await record(terminalRuntime), "1\tgeneration-test\t4\tattention\terror\n")
    await send(terminalRuntime, updated("former-root", "parent"))
    assert.equal(await record(terminalRuntime), "1\tgeneration-test\t5\tready\t-\n")
  } finally {
    await terminalRuntime.cleanup()
  }
})

test("unknown parent fails silent until update establishes correlation", async () => {
  const runtime = await makeRuntime()
  try {
    await send(runtime, created("child", "missing-root"))
    await send(runtime, questionAsked("q-orphan", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t0\tunknown\t-\n")
    await send(runtime, created("root"))
    await send(runtime, updated("child", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t1\tattention\tquestion\n")
  } finally {
    await runtime.cleanup()
  }
})

test("recognized live events without the raw v2 event identity fail to unknown", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    const malformed = questionAsked("q-without-event-id", "root")
    delete malformed.id
    await send(runtime, malformed)
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tunknown\t-\n")
  } finally {
    await runtime.cleanup()
  }
})

test("session deletion recursively clears correlated requests and roots", async () => {
  const runtime = await makeRuntime()
  try {
    await establishReady(runtime)
    await send(runtime, created("child", "root"))
    await send(runtime, permissionAsked("p-child", "child"))
    assert.equal(await record(runtime), "1\tgeneration-test\t2\tattention\tpermission\n")
    await send(runtime, deleted("child", "root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t3\tready\t-\n")
    await send(runtime, deleted("root"))
    assert.equal(await record(runtime), "1\tgeneration-test\t4\tunknown\t-\n")
  } finally {
    await runtime.cleanup()
  }
})

async function startJSONServer(handler) {
  const requests = []
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, "http://127.0.0.1")
    requests.push({
      authorization: request.headers.authorization,
      method: request.method,
      pathname: url.pathname,
      search: url.search,
      directory: url.searchParams.get("directory"),
    })
    try {
      const result = await handler({ request, response, url, number: requests.length })
      const statusCode = result?.statusCode || 200
      const body = result?.raw === true ? String(result.body) : JSON.stringify(result?.body)
      response.writeHead(statusCode, { "content-type": "application/json" })
      response.end(body)
    } catch (error) {
      response.writeHead(500, { "content-type": "application/json" })
      response.end(JSON.stringify({ error: String(error) }))
    }
  })
  await new Promise((resolve, reject) => {
    server.once("error", reject)
    server.listen(0, "127.0.0.1", resolve)
  })
  const address = server.address()
  return {
    requests,
    url: new URL(`http://127.0.0.1:${address.port}/`),
    async close() {
      await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()))
    },
  }
}

function cleanSnapshot(pathname) {
  if (pathname === "/session") return [session("root"), session("child", "root")]
  if (pathname === "/session/status") return { root: { type: "idle" } }
  if (pathname === "/question") return []
  if (pathname === "/permission") return []
  throw new Error(`unexpected endpoint ${pathname}`)
}

function configuredSnapshot(pathname, snapshot) {
  if (pathname === "/session") return snapshot.sessions
  if (pathname === "/session/status") return snapshot.statuses || {}
  if (pathname === "/question") return snapshot.questions || []
  if (pathname === "/permission") return snapshot.permissions || []
  throw new Error(`unexpected endpoint ${pathname}`)
}

async function makeBlockedSnapshotRuntime(snapshot) {
  let releaseStartup
  let releaseSnapshot
  const startupGate = new Promise((resolve) => { releaseStartup = resolve })
  const snapshotGate = new Promise((resolve) => { releaseSnapshot = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 4) await startupGate
    else if (number <= 8) await snapshotGate
    return { body: configuredSnapshot(url.pathname, snapshot) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  await waitFor(() => server.requests.length === 4, "blocked reconciliation startup")
  return {
    releaseSnapshot,
    requests: server.requests,
    runtime,
    async cleanup() {
      releaseStartup()
      releaseSnapshot()
      await new Promise((resolve) => setTimeout(resolve, 50))
      await runtime.cleanup()
      await server.close()
    },
  }
}

async function assertHydrationStaysUnknown(name, responder) {
  const server = await startJSONServer(({ url }) => responder(url.pathname))
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, `${name} requests`)
    await new Promise((resolve) => setTimeout(resolve, 30))
    assert.equal(await record(runtime), "1\tgeneration-test\t0\tunknown\t-\n", name)
  } finally {
    await runtime.cleanup()
    await server.close()
  }
}

test("startup hydrates all four authenticated endpoints with the exact directory query", async () => {
  await pluginModule()
  const directory = "/project with space/?x=1&y=2"
  const expectedSearch = `?directory=${encodeURIComponent(directory).replace(/%20/g, "+")}`
  const expectedAuth = `Basic ${Buffer.from("alice:secret").toString("base64")}`
  const server = await startJSONServer(({ url }) => {
    if (url.pathname === "/session") return { body: [session("root"), session("child", "root")] }
    if (url.pathname === "/session/status") return { body: { root: { type: "busy" } } }
    if (url.pathname === "/question") return { body: [{
      id: "q-hydrated",
      sessionID: "child",
      questions: [{ header: "Choice", question: "Continue?", options: [] }],
    }] }
    if (url.pathname === "/permission") return { body: [{
      id: "p-hydrated",
      sessionID: "root",
      permission: "bash",
      patterns: ["*"],
      metadata: {},
      always: [],
    }] }
    throw new Error(`unexpected endpoint ${url.pathname}`)
  })
  const runtime = await makeRuntime({
    directory,
    serverUrl: server.url,
    env: {
      OPENCODE_SERVER_PASSWORD: "secret",
      OPENCODE_SERVER_USERNAME: "alice",
    },
  })
  try {
    await waitFor(async () => (await record(runtime)).endsWith("\tattention\tquestion\n"), "hydrated question")
    assert.equal(server.requests.length, 4)
    assert.deepEqual(server.requests.map((item) => item.pathname).sort(), [
      "/permission", "/question", "/session", "/session/status",
    ])
    for (const request of server.requests) {
      assert.equal(request.method, "GET")
      assert.equal(request.directory, directory)
      assert.equal(request.search, expectedSearch)
      assert.equal(request.authorization, expectedAuth)
    }
  } finally {
    await runtime.cleanup()
    await server.close()
  }
})

test("hydration rejects a duplicate question request ID", async () => {
  await pluginModule()
  await assertHydrationStaysUnknown("duplicate question ID", (pathname) => {
    if (pathname === "/question") {
      const repeated = { id: "q-repeat", sessionID: "root", questions: [] }
      return { body: [repeated, { ...repeated }] }
    }
    return { body: cleanSnapshot(pathname) }
  })
})

test("hydration rejects a duplicate permission request ID", async () => {
  await pluginModule()
  await assertHydrationStaysUnknown("duplicate permission ID", (pathname) => {
    if (pathname === "/permission") {
      const repeated = {
        id: "p-repeat",
        sessionID: "root",
        permission: "bash",
        patterns: ["*"],
        metadata: {},
        always: [],
      }
      return { body: [repeated, { ...repeated }] }
    }
    return { body: cleanSnapshot(pathname) }
  })
})

const malformedSnapshots = [
  {
    name: "question containing null",
    responder(pathname) {
      if (pathname === "/question") {
        return { body: [{ id: "q-null", sessionID: "root", questions: [null] }] }
      }
      return { body: cleanSnapshot(pathname) }
    },
  },
  {
    name: "question option missing description",
    responder(pathname) {
      if (pathname === "/question") {
        return { body: [{
          id: "q-option",
          sessionID: "root",
          questions: [{ header: "Choice", question: "Continue?", options: [{ label: "Yes" }] }],
        }] }
      }
      return { body: cleanSnapshot(pathname) }
    },
  },
  {
    name: "permission with partial tool identity",
    responder(pathname) {
      if (pathname === "/permission") {
        return { body: [{
          id: "p-tool",
          sessionID: "root",
          permission: "bash",
          patterns: ["*"],
          metadata: {},
          always: [],
          tool: { messageID: "message" },
        }] }
      }
      return { body: cleanSnapshot(pathname) }
    },
  },
  {
    name: "retry with negative attempt",
    responder(pathname) {
      if (pathname === "/session/status") {
        return { body: { root: { type: "retry", attempt: -1, message: "bad", next: 1 } } }
      }
      return { body: cleanSnapshot(pathname) }
    },
  },
]

for (const item of malformedSnapshots) {
  test(`hydration rejects malformed ${item.name}`, async () => {
    await pluginModule()
    await assertHydrationStaysUnknown(item.name, item.responder)
  })
}

test("hydration rejects a model collection beyond its hard limit", async () => {
  await pluginModule()
  const sessions = [session("root")]
  for (let index = 0; index < 4096; index += 1) sessions.push(session(`child-${index}`, "root"))
  await assertHydrationStaysUnknown("oversized session collection", (pathname) => {
    if (pathname === "/session") return { body: sessions }
    return { body: cleanSnapshot(pathname) }
  })
})

test("partial, unauthenticated, or structurally invalid hydration is all-or-nothing unknown", async () => {
  await pluginModule()
  const cases = [
    {
      name: "partial HTTP failure",
      responder(pathname) {
        if (pathname === "/permission") return { statusCode: 503, body: { error: "unavailable" } }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "unauthenticated",
      responder() {
        return { statusCode: 401, body: { error: "unauthorized" } }
      },
    },
    {
      name: "request with missing parent",
      responder(pathname) {
        if (pathname === "/question") return { body: [{ id: "q", sessionID: "missing", questions: [] }] }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "session root is not an array",
      responder(pathname) {
        if (pathname === "/session") return { body: { root: session("root") } }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "session graph is cyclic",
      responder(pathname) {
        if (pathname === "/session") return { body: [session("a", "b"), session("b", "a")] }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "status schema is unknown",
      responder(pathname) {
        if (pathname === "/session/status") return { body: { root: { type: "future" } } }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "question root is not an array",
      responder(pathname) {
        if (pathname === "/question") return { body: { id: "q" } }
        return { body: cleanSnapshot(pathname) }
      },
    },
    {
      name: "permission item is partial",
      responder(pathname) {
        if (pathname === "/permission") return { body: [{ id: "p", sessionID: "root" }] }
        return { body: cleanSnapshot(pathname) }
      },
    },
  ]

  for (const item of cases) {
    await assertHydrationStaysUnknown(item.name, item.responder)
  }
})

test("the next recognized event coalesces a background retry after failed hydration", async () => {
  await pluginModule()
  let round = 0
  const server = await startJSONServer(({ url, number }) => {
    round = Math.max(round, Math.ceil(number / 4))
    if (round === 1 && url.pathname === "/permission") {
      return { statusCode: 503, body: { error: "try again" } }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "failed startup hydration")
    assert.equal(await record(runtime), "1\tgeneration-test\t0\tunknown\t-\n")
    const began = Date.now()
    await Promise.all([
      send(runtime, created("live-root")),
      send(runtime, updated("live-root")),
    ])
    assert.ok(Date.now() - began < 500, "event hook blocked on hydration I/O")
    await waitFor(() => server.requests.length === 8, "recognized-event retry")
    await waitFor(async () => (await record(runtime)).endsWith("\tready\t-\n"), "clean retry state")
    assert.equal(server.requests.length, 8)
  } finally {
    await runtime.cleanup()
    await server.close()
  }
})

test("a newer overlapping hydration attempt wins over an older delayed snapshot", async () => {
  await pluginModule()
  let releaseOld
  const oldGate = new Promise((resolve) => { releaseOld = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 4) {
      await oldGate
      if (url.pathname === "/question") {
        return { body: [{ id: "stale-question", sessionID: "root", questions: [] }] }
      }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "older hydration requests")
    await Promise.all([
      send(runtime, created("root")),
      send(runtime, updated("root")),
    ])
    await waitFor(() => server.requests.length === 8, "newer hydration requests")
    await waitFor(async () => (await record(runtime)).endsWith("\tready\t-\n"), "newer snapshot")
    releaseOld()
    await new Promise((resolve) => setTimeout(resolve, 50))
    assert.ok((await record(runtime)).endsWith("\tready\t-\n"), "older snapshot overwrote the newer attempt")
    assert.equal(server.requests.length, 8, "a superseded attempt requested an unnecessary clean retry")
  } finally {
    releaseOld()
    await runtime.cleanup()
    await server.close()
  }
})

test("a concurrent live mutation fences hydration started by the preceding event", async () => {
  await pluginModule()
  let release
  const gate = new Promise((resolve) => { release = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    await gate
    if (number > 4 && url.pathname === "/session/status") {
      return { body: { root: { type: "busy" } } }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "older hydration requests")
    await Promise.all([
      send(runtime, created("root")),
      send(runtime, status("root", "busy")),
    ])
    await waitFor(() => server.requests.length === 8, "replacement hydration requests")
    assert.ok((await record(runtime)).endsWith("\tunknown\t-\n"), "blocked replacement was not marked dirty")
    release()
    await waitFor(
      async () => (await record(runtime)).endsWith("\tworking\t-\n"),
      "fenced live mutation working state",
    )
  } finally {
    release()
    await runtime.cleanup()
    await server.close()
  }
})

test("an event-raced hydration always receives a clean replacement even while working", async () => {
  await pluginModule()
  let releaseOld
  const oldGate = new Promise((resolve) => { releaseOld = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 4) await oldGate
    if (url.pathname === "/question") {
      return { body: [{
        id: "hydrated-child-question",
        sessionID: "child",
        questions: [{ header: "Choice", question: "Continue?", options: [] }],
      }] }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "older pending-question snapshot")
    await Promise.all([
      send(runtime, created("root")),
      send(runtime, status("root", "busy")),
    ])
    assert.match(await record(runtime), /\t(?:unknown\t-|attention\tquestion)\n$/, "dirty window published a false terminal state")
    await waitFor(() => server.requests.length === 8, "clean replacement hydration")
    await waitFor(async () => (await record(runtime)).endsWith("\tattention\tquestion\n"), "replacement question")
    releaseOld()
    await send(runtime, status("root", "idle"))
    assert.ok((await record(runtime)).endsWith("\tattention\tquestion\n"), "root idle published done over a pending question")
    await new Promise((resolve) => setTimeout(resolve, 50))
    assert.equal(server.requests.length, 8, "event-raced hydration retry did not coalesce")
  } finally {
    releaseOld()
    await runtime.cleanup()
    await server.close()
  }
})

test("a dirty hydration window stays unknown until a clean pending-question snapshot", async () => {
  await pluginModule()
  let releaseStartup
  let releaseFirstRace
  let releaseSecondRace
  const startupGate = new Promise((resolve) => { releaseStartup = resolve })
  const firstRaceGate = new Promise((resolve) => { releaseFirstRace = resolve })
  const secondRaceGate = new Promise((resolve) => { releaseSecondRace = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 4) await startupGate
    else if (number <= 8) await firstRaceGate
    else if (number <= 12) await secondRaceGate
    if (url.pathname === "/question") {
      return { body: [{
        id: "dirty-window-question",
        sessionID: "child",
        questions: [{ header: "Choice", question: "Continue?", options: [] }],
      }] }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "blocked startup hydration")
    await send(runtime, created("root"))
    await waitFor(() => server.requests.length === 8, "blocked first replacement hydration")
    await send(runtime, status("root", "busy"))
    assert.ok((await record(runtime)).endsWith("\tunknown\t-\n"), "root busy did not dirty the blocked replacement")

    releaseFirstRace()
    await waitFor(() => server.requests.length === 12, "blocked hydration after the first event race")
    const beforeIdle = await record(runtime)
    await send(runtime, status("root", "idle"))
    const afterIdle = await record(runtime)
    assert.ok(afterIdle.endsWith("\tunknown\t-\n"), `dirty window published a false completion: ${afterIdle.trim()}`)
    assert.ok(beforeIdle.endsWith("\tunknown\t-\n"), "dirty hydration did not immediately publish unknown")

    releaseSecondRace()
    await waitFor(() => server.requests.length === 16, "clean hydration after the second event race")
    await waitFor(async () => (await record(runtime)).endsWith("\tattention\tquestion\n"), "clean pending question")
    releaseStartup()
    await new Promise((resolve) => setTimeout(resolve, 50))
    assert.equal(server.requests.length, 16, "dirty hydration window retried without another event race")
  } finally {
    releaseStartup()
    releaseFirstRace()
    releaseSecondRace()
    await runtime.cleanup()
    await server.close()
  }
})

test("live events dirty a blocked latest hydration before completion can publish", async () => {
  await pluginModule()
  let releaseStartup
  let releaseBlocked
  const startupGate = new Promise((resolve) => { releaseStartup = resolve })
  const blockedGate = new Promise((resolve) => { releaseBlocked = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 4) await startupGate
    else if (number <= 8) await blockedGate
    if (url.pathname === "/question") {
      return { body: [{
        id: "blocked-window-question",
        sessionID: "child",
        questions: [{ header: "Choice", question: "Continue?", options: [] }],
      }] }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "blocked startup attempt")
    await send(runtime, created("root"))
    await waitFor(() => server.requests.length === 8, "blocked latest hydration")
    await send(runtime, status("root", "busy"))
    const beforeIdle = await record(runtime)
    await send(runtime, status("root", "idle"))
    const afterIdle = await record(runtime)
    assert.ok(afterIdle.endsWith("\tunknown\t-\n"), `blocked dirty attempt published a false completion: ${afterIdle.trim()}`)
    assert.ok(beforeIdle.endsWith("\tunknown\t-\n"), "event did not immediately dirty the blocked latest attempt")

    releaseBlocked()
    await waitFor(() => server.requests.length === 12, "clean hydration after blocked event races")
    await waitFor(async () => (await record(runtime)).endsWith("\tattention\tquestion\n"), "clean blocked-window question")
    releaseStartup()
    await new Promise((resolve) => setTimeout(resolve, 50))
    assert.equal(server.requests.length, 12, "blocked dirty attempt spawned an unrequested retry")
  } finally {
    releaseStartup()
    releaseBlocked()
    await waitFor(() => server.requests.length >= 12, "blocked-test cleanup hydration").catch(() => {})
    await new Promise((resolve) => setTimeout(resolve, 30))
    await runtime.cleanup()
    await server.close()
  }
})

test("a dirty live root error survives a clean idle snapshot", async () => {
  const fixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root")],
    statuses: { root: { type: "idle" } },
  })
  try {
    await send(fixture.runtime, created("root"))
    await waitFor(() => fixture.requests.length === 8, "blocked root-error hydration")
    await send(fixture.runtime, sessionError("dirty-root-error", "root"))
    assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"), "dirty root error escaped suppression")

    fixture.releaseSnapshot()
    await waitFor(() => fixture.requests.length === 12, "clean root-error hydration")
    await waitFor(
      async () => (await record(fixture.runtime)).endsWith("\tattention\terror\n"),
      "reconciled root error",
    )
  } finally {
    await fixture.cleanup()
  }
})

test("a dirty live root busy-to-idle completion survives a clean idle snapshot", async () => {
  const fixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root")],
    statuses: { root: { type: "idle" } },
  })
  try {
    await send(fixture.runtime, created("root"))
    await waitFor(() => fixture.requests.length === 8, "blocked root-completion hydration")
    await send(fixture.runtime, status("root", "busy"))
    assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"), "dirty root busy escaped suppression")
    await send(fixture.runtime, status("root", "idle"))
    assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"), "dirty root idle escaped suppression")

    fixture.releaseSnapshot()
    await waitFor(() => fixture.requests.length === 12, "clean root-completion hydration")
    await waitFor(
      async () => (await record(fixture.runtime)).endsWith("\tattention\tdone\n"),
      "reconciled root completion",
    )
  } finally {
    await fixture.cleanup()
  }
})

test("dirty live armed roots reconcile only to safe clean snapshots", async () => {
  const idleSnapshot = {
    sessions: [session("root")],
    statuses: { root: { type: "idle" } },
  }
  const initialServer = await startJSONServer(({ url }) => ({
    body: configuredSnapshot(url.pathname, idleSnapshot),
  }))
  const initialRuntime = await makeRuntime({ serverUrl: initialServer.url })
  try {
    await waitFor(() => initialServer.requests.length === 4, "initial clean idle hydration")
    await waitFor(
      async () => (await record(initialRuntime)).endsWith("\tready\t-\n"),
      "unarmed initial idle state",
    )
  } finally {
    await initialRuntime.cleanup()
    await initialServer.close()
  }

  async function exercise(name, snapshot, events, verify) {
    const fixture = await makeBlockedSnapshotRuntime(snapshot)
    try {
      await send(fixture.runtime, created("root"))
      await waitFor(() => fixture.requests.length === 8, `blocked ${name} hydration`)
      await send(fixture.runtime, status("root", "busy"))
      for (const value of events) await send(fixture.runtime, value)
      assert.ok(
        (await record(fixture.runtime)).endsWith("\tunknown\t-\n"),
        `${name} dirty armed root escaped suppression`,
      )
      fixture.releaseSnapshot()
      await waitFor(() => fixture.requests.length === 12, `clean ${name} hydration`)
      await verify(fixture.runtime)
    } finally {
      await fixture.cleanup()
    }
  }

  await exercise("idle", idleSnapshot, [], async (runtime) => {
    await waitFor(
      async () => (await record(runtime)).endsWith("\tattention\tdone\n"),
      "snapshot-observed armed-root completion",
    )
  })

  for (const kind of ["question", "permission"]) {
    const requestID = `armed-${kind}`
    const snapshot = {
      ...idleSnapshot,
      ...(kind === "question"
        ? {
            questions: [{
              id: requestID,
              sessionID: "root",
              questions: [{ header: "Choice", question: "Continue?", options: [] }],
            }],
          }
        : {
            permissions: [{
              id: requestID,
              sessionID: "root",
              permission: "bash",
              patterns: ["*"],
              metadata: {},
              always: [],
            }],
          }),
    }
    await exercise(`pending ${kind}`, snapshot, [], async (runtime) => {
      await waitFor(
        async () => (await record(runtime)).endsWith(`\tattention\t${kind}\n`),
        `armed-root snapshot ${kind}`,
      )
      await send(runtime, kind === "question"
        ? questionReplied(requestID, "root")
        : permissionReplied(requestID, "root"))
      assert.ok(
        (await record(runtime)).endsWith("\tready\t-\n"),
        `clearing the ${kind} revealed a hidden armed-root completion`,
      )
    })
  }

  const parentIdle = {
    sessions: [session("parent")],
    statuses: { parent: { type: "idle" } },
  }
  const absentCases = [
    { name: "snapshot-deleted root", snapshot: parentIdle, events: [] },
    {
      name: "snapshot-reparented root",
      snapshot: {
        sessions: [session("parent"), session("root", "parent")],
        statuses: { parent: { type: "idle" } },
      },
      events: [],
    },
    { name: "live-deleted root", snapshot: idleSnapshot, events: [deleted("root")] },
    {
      name: "live-reparented root",
      snapshot: {
        sessions: [session("root"), session("parent")],
        statuses: { parent: { type: "idle" }, root: { type: "idle" } },
      },
      events: [created("parent"), updated("root", "parent")],
    },
  ]
  for (const value of absentCases) {
    await exercise(value.name, value.snapshot, value.events, async (runtime) => {
      await waitFor(
        async () => (await record(runtime)).endsWith("\tready\t-\n"),
        `${value.name} clean state`,
      )
    })
  }

  for (const snapshotStatus of ["busy", "retry"]) {
    const snapshot = {
      sessions: [session("root")],
      statuses: {
        root: snapshotStatus === "retry"
          ? { type: "retry", attempt: 2, message: "retrying", next: Date.now() + 1000 }
          : { type: "busy" },
      },
    }
    await exercise(`snapshot ${snapshotStatus}`, snapshot, [], async (runtime) => {
      await waitFor(
        async () => (await record(runtime)).endsWith("\tworking\t-\n"),
        `armed-root snapshot ${snapshotStatus} working state`,
      )
      await send(runtime, status("root", "idle"))
      assert.ok(
        (await record(runtime)).endsWith("\tattention\tdone\n"),
        `${snapshotStatus} snapshot did not keep the root armed`,
      )
    })
  }
})

test("clean busy and retry snapshots clear dirty terminal facts", async () => {
  for (const snapshotStatus of ["busy", "retry"]) {
    const fixture = await makeBlockedSnapshotRuntime({
      sessions: [session("root")],
      statuses: {
        root: snapshotStatus === "retry"
          ? { type: "retry", attempt: 2, message: "retrying", next: Date.now() + 1000 }
          : { type: "busy" },
      },
    })
    try {
      await send(fixture.runtime, created("root"))
      await waitFor(() => fixture.requests.length === 8, `blocked ${snapshotStatus} hydration`)
      await send(fixture.runtime, sessionError(`dirty-${snapshotStatus}-error`, "root"))
      assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"))

      fixture.releaseSnapshot()
      await waitFor(() => fixture.requests.length === 12, `clean ${snapshotStatus} hydration`)
      await waitFor(
        async () => (await record(fixture.runtime)).endsWith("\tworking\t-\n"),
        `clean ${snapshotStatus} working state`,
      )
      await send(fixture.runtime, status("root", "idle"))
      assert.ok(
        (await record(fixture.runtime)).endsWith("\tattention\tdone\n"),
        `${snapshotStatus} snapshot retained a stale error`,
      )
    } finally {
      await fixture.cleanup()
    }
  }
})

test("a clean pending request discards a hidden dirty completion", async () => {
  const fixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root")],
    statuses: { root: { type: "idle" } },
    questions: [{
      id: "snapshot-question",
      sessionID: "root",
      questions: [{ header: "Choice", question: "Continue?", options: [] }],
    }],
  })
  try {
    await send(fixture.runtime, created("root"))
    await waitFor(() => fixture.requests.length === 8, "blocked pending-request hydration")
    await send(fixture.runtime, status("root", "busy"))
    await send(fixture.runtime, status("root", "idle"))
    assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"), "dirty completion escaped suppression")

    fixture.releaseSnapshot()
    await waitFor(() => fixture.requests.length === 12, "clean pending-request hydration")
    await waitFor(
      async () => (await record(fixture.runtime)).endsWith("\tattention\tquestion\n"),
      "snapshot question",
    )
    await send(fixture.runtime, questionReplied("snapshot-question", "root"))
    assert.ok(
      (await record(fixture.runtime)).endsWith("\tready\t-\n"),
      "clearing the snapshot question revealed a hidden completion",
    )
  } finally {
    await fixture.cleanup()
  }
})

test("deleted and reparented dirty roots do not carry terminal facts", async () => {
  const deletedFixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root")],
    statuses: { root: { type: "idle" } },
  })
  try {
    await send(deletedFixture.runtime, created("root"))
    await waitFor(() => deletedFixture.requests.length === 8, "blocked deleted-root hydration")
    await send(deletedFixture.runtime, sessionError("deleted-root-error", "root"))
    await send(deletedFixture.runtime, deleted("root"))
    deletedFixture.releaseSnapshot()
    await waitFor(() => deletedFixture.requests.length === 12, "clean deleted-root hydration")
    await waitFor(
      async () => (await record(deletedFixture.runtime)).endsWith("\tready\t-\n"),
      "deleted-root clean state",
    )
  } finally {
    await deletedFixture.cleanup()
  }

  const reparentedFixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root"), session("parent")],
    statuses: { parent: { type: "idle" }, root: { type: "idle" } },
  })
  try {
    await send(reparentedFixture.runtime, created("root"))
    await waitFor(() => reparentedFixture.requests.length === 8, "blocked reparented-root hydration")
    await send(reparentedFixture.runtime, created("parent"))
    await send(reparentedFixture.runtime, sessionError("reparented-root-error", "root"))
    await send(reparentedFixture.runtime, updated("root", "parent"))
    reparentedFixture.releaseSnapshot()
    await waitFor(() => reparentedFixture.requests.length === 12, "clean reparented-root hydration")
    await waitFor(
      async () => (await record(reparentedFixture.runtime)).endsWith("\tready\t-\n"),
      "reparented-root clean state",
    )
  } finally {
    await reparentedFixture.cleanup()
  }
})

test("unknown-session errors correlate only to a clean top-level root", async () => {
  const cases = [
    {
      name: "top-level root",
      snapshot: {
        sessions: [session("mystery")],
        statuses: { mystery: { type: "idle" } },
      },
      suffix: "\tattention\terror\n",
    },
    {
      name: "child session",
      snapshot: {
        sessions: [session("root"), session("mystery", "root")],
        statuses: { root: { type: "idle" } },
      },
      suffix: "\tready\t-\n",
    },
    {
      name: "deleted session",
      snapshot: {
        sessions: [session("root")],
        statuses: { root: { type: "idle" } },
      },
      suffix: "\tready\t-\n",
    },
  ]

  for (const value of cases) {
    const fixture = await makeBlockedSnapshotRuntime(value.snapshot)
    try {
      await send(fixture.runtime, sessionError(`unknown-${value.name.replace(" ", "-")}`, "mystery", unknownError()))
      await waitFor(() => fixture.requests.length === 8, `blocked unknown-error ${value.name} hydration`)
      assert.ok((await record(fixture.runtime)).endsWith("\tunknown\t-\n"), `${value.name} error did not fail silent`)
      fixture.releaseSnapshot()
      await waitFor(
        async () => (await record(fixture.runtime)).endsWith(value.suffix),
        `clean unknown-error ${value.name} state`,
      )
    } finally {
      await fixture.cleanup()
    }
  }
})

test("terminal serials remain monotonic across clean candidate replacement", async () => {
  const fixture = await makeBlockedSnapshotRuntime({
    sessions: [session("root-a"), session("root-b")],
    statuses: { "root-a": { type: "idle" }, "root-b": { type: "idle" } },
  })
  try {
    await send(fixture.runtime, created("root-a"))
    await waitFor(() => fixture.requests.length === 8, "blocked serial hydration")
    await send(fixture.runtime, created("root-b"))
    await send(fixture.runtime, sessionError("serial-error-a", "root-a"))
    await send(fixture.runtime, sessionError("serial-error-b", "root-b"))
    fixture.releaseSnapshot()
    await waitFor(() => fixture.requests.length === 12, "clean serial hydration")
    await waitFor(
      async () => (await record(fixture.runtime)).endsWith("\tattention\terror\n"),
      "carried serial errors",
    )
    const before = BigInt((await record(fixture.runtime)).trimEnd().split("\t")[2])

    await send(fixture.runtime, sessionError("serial-error-a-new", "root-a"))
    const after = BigInt((await record(fixture.runtime)).trimEnd().split("\t")[2])
    assert.equal(after, before + 1n, "candidate replacement reset the terminal serial counter")
  } finally {
    await fixture.cleanup()
  }
})

test("one failed HTTP endpoint aborts its hanging hydration siblings", async () => {
  await pluginModule()
  let release
  const gate = new Promise((resolve) => { release = resolve })
  let closed = 0
  const server = await startJSONServer(async ({ response, url }) => {
    if (url.pathname === "/permission") return { statusCode: 503, body: { error: "failed" } }
    response.once("close", () => { closed += 1 })
    await gate
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "mixed hydration requests")
    await waitFor(() => closed === 3, "aborted sibling sockets", 2500)
    assert.equal(await record(runtime), "1\tgeneration-test\t0\tunknown\t-\n")
  } finally {
    release()
    await runtime.cleanup()
    await server.close()
  }
})

test("hung HTTP attempts time out, stay nonblocking, and do not grow without events", async () => {
  await pluginModule()
  let release
  const gate = new Promise((resolve) => { release = resolve })
  let closed = 0
  const server = await startJSONServer(async ({ response, url }) => {
    response.once("close", () => { closed += 1 })
    await gate
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "hung startup requests")
    await waitFor(() => closed === 4, "transport deadline", 3000)
    await new Promise((resolve) => setTimeout(resolve, 100))
    assert.equal(server.requests.length, 4, "failed hydration retried without a delivered event")
    const began = Date.now()
    await send(runtime, created("retry-root"))
    assert.ok(Date.now() - began < 500, "recognized event waited for hung HTTP")
    await waitFor(() => server.requests.length === 8, "single hung background retry")
    await waitFor(() => closed === 8, "retry transport deadline", 3000)
    await new Promise((resolve) => setTimeout(resolve, 100))
    assert.equal(server.requests.length, 8, "hung retry spawned additional attempts")
  } finally {
    release()
    await runtime.cleanup()
    await server.close()
  }
})

test("dripping HTTP responses cannot outlive the absolute transport deadline", async () => {
  await pluginModule()
  let closed = 0
  const responses = new Set()
  const server = await startJSONServer(async ({ response }) => {
    responses.add(response)
    response.writeHead(200, { "content-type": "application/json" })
    response.write("[")
    const interval = setInterval(() => response.write(" "), 100)
    response.once("close", () => {
      clearInterval(interval)
      responses.delete(response)
      closed += 1
    })
    await new Promise(() => {})
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "dripping hydration requests")
    await waitFor(() => closed === 4, "absolute transport deadline", 3000)
    assert.equal(await record(runtime), "1\tgeneration-test\t0\tunknown\t-\n")
    await new Promise((resolve) => setTimeout(resolve, 100))
    assert.equal(server.requests.length, 4, "absolute deadline spawned an unrequested retry")
  } finally {
    for (const response of responses) response.destroy()
    await runtime.cleanup()
    await server.close()
  }
})

test("hydration epoch prevents a delayed snapshot from overwriting live events", async () => {
  await pluginModule()
  let releaseFirst
  let releaseSecond
  const firstGate = new Promise((resolve) => { releaseFirst = resolve })
  const secondGate = new Promise((resolve) => { releaseSecond = resolve })
  const server = await startJSONServer(async ({ url, number }) => {
    if (number <= 8) await firstGate
    else if (number <= 12) await secondGate
    if (number > 12 && url.pathname === "/session/status") {
      return { body: { root: { type: "busy" } } }
    }
    return { body: cleanSnapshot(url.pathname) }
  })
  const runtime = await makeRuntime({ serverUrl: server.url })
  try {
    await waitFor(() => server.requests.length === 4, "delayed startup requests")
    await send(runtime, created("root"))
    await send(runtime, status("root", "busy"))
    assert.ok((await record(runtime)).endsWith("\tunknown\t-\n"))
    releaseFirst()
    await waitFor(() => server.requests.length === 12, "clean post-race hydration")
    await send(runtime, updated("root"))
    releaseSecond()
    await waitFor(() => server.requests.length === 16, "clean hydration after a repeated event race")
    await waitFor(async () => (await record(runtime)).endsWith("\tworking\t-\n"), "clean repeated-race working state")
    await new Promise((resolve) => setTimeout(resolve, 50))
    assert.equal(server.requests.length, 16, "clean post-race hydration retried without another event race")
  } finally {
    releaseFirst()
    releaseSecond()
    await runtime.cleanup()
    await server.close()
  }
})

test("concurrent readers see only complete old or new atomic records", async () => {
  const runtime = await makeRuntime()
  let reading = true
  const observed = new Set()
  try {
    await send(runtime, created("root"))
    const reader = (async () => {
      while (reading) {
        observed.add(await record(runtime))
        await new Promise((resolve) => setImmediate(resolve))
      }
    })()
    for (let turn = 0; turn < 25; turn += 1) {
      await send(runtime, status("root", "busy"))
      await send(runtime, status("root", "idle"))
    }
    reading = false
    await reader
    assert.ok(observed.size > 1, "reader did not overlap writes")
    for (const value of observed) {
      assert.match(value, /^1\tgeneration-test\t\d+\t(?:unknown\t-|working\t-|attention\tdone)\n$/)
    }
  } finally {
    reading = false
    await runtime.cleanup()
  }
})

let failures = 0
for (const { name, fn } of tests) {
  try {
    await fn()
    process.stdout.write(`ok - ${name}\n`)
  } catch (error) {
    failures += 1
    process.stderr.write(`not ok - ${name}\n${error?.stack || error}\n`)
  }
}

if (failures > 0) {
  process.stderr.write(`${failures} of ${tests.length} OpenCode plugin contract tests failed\n`)
  process.exitCode = 1
}
