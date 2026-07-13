import { randomBytes } from "node:crypto"
import { open, rename, stat, unlink } from "node:fs/promises"
import { request as httpRequest } from "node:http"
import { request as httpsRequest } from "node:https"
import { basename, dirname, join } from "node:path"

const MAX_STATE_BYTES = 4096
const MAX_RESPONSE_BYTES = 1024 * 1024
const MAX_IDENTIFIER_BYTES = 256
const MAX_SCHEMA_STRING_BYTES = 64 * 1024
const MAX_NESTED_ITEMS = 256
const MAX_MODEL_ENTRIES = 4096
const MAX_ERROR_IDENTITIES = 4096
const HTTP_TIMEOUT_MS = 1000
const MAX_SEQUENCE = (1n << 64n) - 1n
const EVENT_TYPES = new Set([
  "question.asked",
  "question.replied",
  "question.rejected",
  "permission.asked",
  "permission.replied",
  "session.status",
  "session.error",
  "session.created",
  "session.updated",
  "session.deleted",
])

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value)
}

function boundedString(value, maximum = MAX_SCHEMA_STRING_BYTES) {
  return typeof value === "string" && Buffer.byteLength(value) <= maximum
}

function isIdentifier(value) {
  return boundedString(value, MAX_IDENTIFIER_BYTES) && value.length > 0 && !/[\t\r\n]/.test(value)
}

function validGeneration(value) {
  return isIdentifier(value)
}

function validStatus(value) {
  if (!isObject(value)) return false
  if (value.type === "idle" || value.type === "busy") return true
  return value.type === "retry" &&
    Number.isSafeInteger(value.attempt) && value.attempt >= 0 &&
    boundedString(value.message) &&
    Number.isSafeInteger(value.next) && value.next >= 0 &&
    (value.action === undefined || (
      isObject(value.action) &&
      boundedString(value.action.reason) &&
      boundedString(value.action.provider) &&
      boundedString(value.action.title) &&
      boundedString(value.action.message) &&
      boundedString(value.action.label) &&
      (value.action.link === undefined || boundedString(value.action.link))
    ))
}

function validSession(value) {
  if (!isObject(value) || !isIdentifier(value.id)) return false
  return value.parentID === undefined || isIdentifier(value.parentID)
}

function validTool(value) {
  return isObject(value) && isIdentifier(value.messageID) && isIdentifier(value.callID)
}

function validQuestionInfo(value) {
  return isObject(value) && boundedString(value.header) && boundedString(value.question) &&
    Array.isArray(value.options) && value.options.length <= MAX_NESTED_ITEMS &&
    value.options.every((option) => isObject(option) &&
      boundedString(option.label) && boundedString(option.description)) &&
    (value.multiple === undefined || typeof value.multiple === "boolean") &&
    (value.custom === undefined || typeof value.custom === "boolean")
}

function validQuestion(value) {
  return isObject(value) && isIdentifier(value.id) && isIdentifier(value.sessionID) &&
    Array.isArray(value.questions) && value.questions.length <= MAX_NESTED_ITEMS &&
    value.questions.every(validQuestionInfo) &&
    (value.tool === undefined || validTool(value.tool))
}

function validAnswers(value) {
  return Array.isArray(value) && value.length <= MAX_NESTED_ITEMS &&
    value.every((answer) => Array.isArray(answer) && answer.length <= MAX_NESTED_ITEMS &&
      answer.every((item) => boundedString(item)))
}

function validPermission(value) {
  return isObject(value) && isIdentifier(value.id) &&
    isIdentifier(value.sessionID) && boundedString(value.permission) &&
    Array.isArray(value.patterns) && value.patterns.length <= MAX_NESTED_ITEMS &&
    value.patterns.every((item) => boundedString(item)) &&
    isObject(value.metadata) && Object.keys(value.metadata).length <= MAX_NESTED_ITEMS &&
    Object.keys(value.metadata).every((key) => boundedString(key)) &&
    Array.isArray(value.always) && value.always.length <= MAX_NESTED_ITEMS &&
    value.always.every((item) => boundedString(item)) &&
    (value.tool === undefined || validTool(value.tool))
}

function validStringRecord(value) {
  return isObject(value) && Object.keys(value).length <= MAX_NESTED_ITEMS &&
    Object.entries(value).every(([key, item]) => boundedString(key) && boundedString(item))
}

function validError(value) {
  if (!isObject(value) || !isObject(value.data)) return false
  const data = value.data
  switch (value.name) {
    case "ProviderAuthError":
      return isIdentifier(data.providerID) && boundedString(data.message)
    case "UnknownError":
      return boundedString(data.message) &&
        (data.ref === undefined || boundedString(data.ref))
    case "MessageOutputLengthError":
      return true
    case "MessageAbortedError":
    case "ContentFilterError":
      return boundedString(data.message)
    case "StructuredOutputError":
      return boundedString(data.message) &&
        Number.isSafeInteger(data.retries) && data.retries >= 0
    case "ContextOverflowError":
      return boundedString(data.message) &&
        (data.responseBody === undefined || boundedString(data.responseBody))
    case "APIError":
      return boundedString(data.message) && typeof data.isRetryable === "boolean" &&
        (data.statusCode === undefined || (Number.isSafeInteger(data.statusCode) && data.statusCode >= 0)) &&
        (data.responseBody === undefined || boundedString(data.responseBody)) &&
        (data.responseHeaders === undefined || validStringRecord(data.responseHeaders)) &&
        (data.metadata === undefined || validStringRecord(data.metadata))
    default:
      return false
  }
}

function parseState(data, generation) {
  if (data.length === 0 || data.length > MAX_STATE_BYTES || data.includes("\r")) return null
  if (!data.endsWith("\n") || data.indexOf("\n") !== data.length - 1) return null
  const fields = data.slice(0, -1).split("\t")
  if (fields.length !== 5 || fields[0] !== "1" || fields[1] !== generation) return null
  if (!/^(?:0|[1-9][0-9]*)$/.test(fields[2])) return null
  let sequence
  try {
    sequence = BigInt(fields[2])
  } catch {
    return null
  }
  if (sequence > MAX_SEQUENCE) return null
  const phase = fields[3]
  const reason = fields[4]
  if (["ready", "working", "unknown"].includes(phase)) {
    if (reason !== "-") return null
  } else if (phase === "attention") {
    if (!["done", "question", "permission", "error"].includes(reason)) return null
  } else {
    return null
  }
  return { phase, reason, sequence }
}

async function boundedStateRead(path, generation) {
  let handle
  try {
    handle = await open(path, "r")
    const buffer = Buffer.alloc(MAX_STATE_BYTES + 1)
    const result = await handle.read(buffer, 0, buffer.length, 0)
    if (result.bytesRead > MAX_STATE_BYTES) return null
    return parseState(buffer.subarray(0, result.bytesRead).toString("utf8"), generation)
  } catch {
    return null
  } finally {
    if (handle) await handle.close().catch(() => {})
  }
}

async function createPublisher(path, generation) {
  if (!path || !validGeneration(generation)) return null
  const parent = dirname(path)
  let parentDevice
  let parentInode
  try {
    const info = await stat(parent, { bigint: true })
    if (!info.isDirectory()) return null
    parentDevice = info.dev
    parentInode = info.ino
  } catch {
    return null
  }

  const recovered = await boundedStateRead(path, generation)
  let current = recovered || { phase: "unknown", reason: "-", sequence: 0n }
  let hasState = recovered !== null
  let currentIdentity = ""
  let identityKnown = recovered === null || recovered.phase !== "attention"
  let fenced = false

  async function parentMatches() {
    if (fenced) return false
    try {
      const info = await stat(parent, { bigint: true })
      if (!info.isDirectory() || info.dev !== parentDevice || info.ino !== parentInode) {
        fenced = true
        return false
      }
      return true
    } catch {
      fenced = true
      return false
    }
  }

  async function replace(record) {
    if (!await parentMatches()) return false

    const temporary = join(parent, `.${basename(path)}.${process.pid}.${randomBytes(8).toString("hex")}`)
    let handle
    try {
      handle = await open(temporary, "wx", 0o600)
      await handle.chmod(0o600)
      await handle.writeFile(record, "utf8")
      await handle.close()
      handle = null
      if (!await parentMatches()) return false
      await rename(temporary, path)
      return true
    } catch (error) {
      if (error && (error.code === "ENOENT" || error.code === "ENOTDIR")) fenced = true
      return false
    } finally {
      if (handle) await handle.close().catch(() => {})
      await unlink(temporary).catch(() => {})
    }
  }

  async function publish(phase, reason, identity = "") {
    if (fenced) return false
    const semanticChanged = !hasState || current.phase !== phase || current.reason !== reason
    if (!semanticChanged && phase === "attention" && identity) {
      if (!identityKnown) {
        currentIdentity = identity
        identityKnown = true
        return true
      }
      if (identity === currentIdentity) return true
    } else if (!semanticChanged) {
      return true
    }

    if (current.sequence >= MAX_SEQUENCE) return false
    const sequence = current.sequence + 1n
    const record = `1\t${generation}\t${sequence}\t${phase}\t${reason}\n`
    if (Buffer.byteLength(record) > MAX_STATE_BYTES || !await replace(record)) return false
    current = { phase, reason, sequence }
    hasState = true
    if (phase === "attention" && identity) {
      currentIdentity = identity
      identityKnown = true
    } else if (phase === "attention") {
      currentIdentity = ""
      identityKnown = false
    } else {
      currentIdentity = ""
      identityKnown = true
    }
    return true
  }

  return { publish }
}

function newModel() {
  return {
    dirty: false,
    invalid: false,
    sessions: new Map(),
    statuses: new Map(),
    armed: new Set(),
    terminal: new Map(),
    pendingErrors: new Map(),
    seenErrors: new Map(),
    questions: new Map(),
    permissions: new Map(),
    order: 0,
    serial: 0,
  }
}

function rootFor(model, sessionID) {
  let cursor = sessionID
  const seen = new Set()
  while (true) {
    if (seen.has(cursor)) return null
    seen.add(cursor)
    const info = model.sessions.get(cursor)
    if (!info) return null
    if (info.parentID === undefined) return cursor
    cursor = info.parentID
  }
}

function sessionGraph(model) {
  const roots = []
  let unresolved = false
  for (const sessionID of model.sessions.keys()) {
    const root = rootFor(model, sessionID)
    if (!root) unresolved = true
    else if (root === sessionID) roots.push(root)
  }
  for (const sessionID of model.statuses.keys()) {
    if (!rootFor(model, sessionID)) unresolved = true
  }
  for (const sessionID of model.pendingErrors.keys()) {
    if (!rootFor(model, sessionID)) unresolved = true
  }
  return { roots, unresolved }
}

function correlatedEntries(model, entries) {
  const values = []
  let unresolved = false
  for (const entry of entries.values()) {
    const root = rootFor(model, entry.sessionID)
    if (!root) unresolved = true
    else values.push({ entry, root })
  }
  return { values, unresolved }
}

function newestUnannounced(values) {
  let selected = null
  for (const value of values) {
    if (!value.entry.announced && (!selected || value.entry.order > selected.entry.order)) selected = value
  }
  return selected
}

function chooseState(model) {
  if (model.dirty || model.invalid) return { phase: "unknown", reason: "-", identity: "" }
  const graph = sessionGraph(model)
  const questions = correlatedEntries(model, model.questions)
  const permissions = correlatedEntries(model, model.permissions)
  if (graph.unresolved || questions.unresolved || permissions.unresolved) {
    return { phase: "unknown", reason: "-", identity: "" }
  }

  if (questions.values.length > 0) {
    const selected = newestUnannounced(questions.values)
    return {
      phase: "attention",
      reason: "question",
      identity: selected ? `question:${selected.entry.id}` : "",
      mark: selected ? selected.entry : null,
    }
  }
  if (permissions.values.length > 0) {
    const selected = newestUnannounced(permissions.values)
    return {
      phase: "attention",
      reason: "permission",
      identity: selected ? `permission:${selected.entry.id}` : "",
      mark: selected ? selected.entry : null,
    }
  }

  const terminals = [...model.terminal.values()]
  const errors = terminals.filter((item) => item.type === "error")
  if (errors.length > 0) {
    const selected = errors.reduce((left, right) => left.serial > right.serial ? left : right)
    return { phase: "attention", reason: "error", identity: selected.identity }
  }
  const completions = terminals.filter((item) => item.type === "done")
  if (completions.length > 0) {
    const selected = completions.reduce((left, right) => left.serial > right.serial ? left : right)
    return { phase: "attention", reason: "done", identity: selected.identity }
  }

  for (const root of graph.roots) {
    const value = model.statuses.get(root)
    if (value && (value.type === "busy" || value.type === "retry")) {
      return { phase: "working", reason: "-", identity: "" }
    }
  }
  if (graph.roots.length === 0) return { phase: "unknown", reason: "-", identity: "" }
  if (graph.roots.every((root) => model.statuses.get(root)?.type === "idle")) {
    return { phase: "ready", reason: "-", identity: "" }
  }
  return { phase: "unknown", reason: "-", identity: "" }
}

async function emit(model, publisher) {
  const state = chooseState(model)
  const accepted = await publisher.publish(state.phase, state.reason, state.identity)
  if (accepted && state.mark) state.mark.announced = true
}

function hasPendingForRoot(model, root) {
  for (const pending of [model.questions, model.permissions]) {
    for (const item of pending.values()) {
      const pendingRoot = rootFor(model, item.sessionID)
      if (!pendingRoot || pendingRoot === root) return true
    }
  }
  return false
}

function removeSessionTree(model, sessionID) {
  const removed = new Set([sessionID])
  let changed = true
  while (changed) {
    changed = false
    for (const [id, info] of model.sessions) {
      if (info.parentID !== undefined && removed.has(info.parentID) && !removed.has(id)) {
        removed.add(id)
        changed = true
      }
    }
  }
  for (const id of removed) {
    model.sessions.delete(id)
    model.statuses.delete(id)
    model.armed.delete(id)
    model.terminal.delete(id)
    model.pendingErrors.delete(id)
  }
  for (const pending of [model.questions, model.permissions]) {
    for (const [id, item] of pending) {
      if (removed.has(item.sessionID)) pending.delete(id)
    }
  }
}

function rememberError(model, eventID) {
  if (model.seenErrors.has(eventID)) return "duplicate"
  if (model.seenErrors.size >= MAX_ERROR_IDENTITIES) return "overflow"
  model.seenErrors.set(eventID, true)
  return "added"
}

function reconcileCorrelations(model) {
  for (const root of [...model.armed]) {
    if (rootFor(model, root) !== root) model.armed.delete(root)
  }
  for (const root of [...model.terminal.keys()]) {
    if (rootFor(model, root) !== root) model.terminal.delete(root)
  }
  for (const [sessionID, value] of model.statuses) {
    if (rootFor(model, sessionID) !== sessionID) continue
    if ((value.type === "busy" || value.type === "retry") && !model.terminal.has(sessionID)) {
      model.armed.add(sessionID)
    }
  }
  for (const [sessionID, pending] of [...model.pendingErrors]) {
    const root = rootFor(model, sessionID)
    if (!root) continue
    model.pendingErrors.delete(sessionID)
    if (root !== sessionID) continue
    model.armed.delete(root)
    model.terminal.set(root, pending)
  }
}

function sessionProperties(value) {
  if (!isObject(value) || !isIdentifier(value.sessionID) || !validSession(value.info)) return null
  if (value.sessionID !== value.info.id) return null
  return { id: value.sessionID, parentID: value.info.parentID }
}

function addPending(model, pending, value) {
  const previous = pending.get(value.id)
  if (previous) return previous.sessionID === value.sessionID
  if (pending.size >= MAX_MODEL_ENTRIES || model.order >= Number.MAX_SAFE_INTEGER) return false
  model.order += 1
  pending.set(value.id, {
    announced: false,
    id: value.id,
    order: model.order,
    sessionID: value.sessionID,
  })
  return true
}

function clearPending(model, pending, properties) {
  if (!isObject(properties) || !isIdentifier(properties.requestID) || !isIdentifier(properties.sessionID)) return false
  const previous = pending.get(properties.requestID)
  if (previous && previous.sessionID !== properties.sessionID) return false
  pending.delete(properties.requestID)
  return true
}

async function applyEvent(model, publisher, value) {
  const properties = value.properties
  let shouldEmit = true
  switch (value.type) {
    case "session.created":
    case "session.updated": {
      const info = sessionProperties(properties)
      if (!info) {
        model.invalid = true
        break
      }
      if (!model.sessions.has(info.id) && model.sessions.size >= MAX_MODEL_ENTRIES) {
        model.invalid = true
        break
      }
      model.sessions.set(info.id, info)
      reconcileCorrelations(model)
      shouldEmit = model.statuses.size > 0 || model.questions.size > 0 ||
        model.permissions.size > 0 || model.terminal.size > 0 || model.pendingErrors.size > 0
      break
    }
    case "session.deleted": {
      const info = sessionProperties(properties)
      if (!info) model.invalid = true
      else removeSessionTree(model, info.id)
      break
    }
    case "session.status": {
      if (!isObject(properties) || !isIdentifier(properties.sessionID) || !validStatus(properties.status)) {
        model.invalid = true
        break
      }
      if (!model.statuses.has(properties.sessionID) && model.statuses.size >= MAX_MODEL_ENTRIES) {
        model.invalid = true
        break
      }
      model.statuses.set(properties.sessionID, { type: properties.status.type })
      const root = rootFor(model, properties.sessionID)
      if (!root) break
      if (root !== properties.sessionID) break
      if (properties.status.type === "busy" || properties.status.type === "retry") {
        model.armed.add(root)
        model.terminal.delete(root)
      } else {
        const terminal = model.terminal.get(root)
        if (terminal?.type === "error" && terminal.suppressIdle) {
          terminal.suppressIdle = false
          model.armed.delete(root)
        } else if (model.armed.has(root)) {
          model.armed.delete(root)
          if (!hasPendingForRoot(model, root)) {
            if (model.serial >= Number.MAX_SAFE_INTEGER) {
              model.invalid = true
              break
            }
            model.serial += 1
            model.terminal.set(root, {
              identity: `done:${root}:${model.serial}`,
              serial: model.serial,
              type: "done",
            })
          }
        }
      }
      break
    }
    case "session.error": {
      if (!isIdentifier(value.id) || !isObject(properties) ||
        !isIdentifier(properties.sessionID) || !validError(properties.error)) {
        model.invalid = true
        break
      }
      const remembered = rememberError(model, value.id)
      if (remembered === "duplicate") {
        shouldEmit = false
        break
      }
      if (remembered === "overflow") {
        model.invalid = true
        break
      }
      if (model.serial >= Number.MAX_SAFE_INTEGER) {
        model.invalid = true
        break
      }
      model.serial += 1
      const terminal = {
        identity: `error:${properties.sessionID}:${value.id}`,
        serial: model.serial,
        suppressIdle: true,
        type: "error",
      }
      const root = rootFor(model, properties.sessionID)
      if (!root) {
        if (!model.pendingErrors.has(properties.sessionID) &&
          model.pendingErrors.size >= MAX_MODEL_ENTRIES) {
          model.invalid = true
          break
        }
        model.pendingErrors.set(properties.sessionID, terminal)
        break
      }
      if (root !== properties.sessionID) break
      model.armed.delete(root)
      model.terminal.set(root, terminal)
      break
    }
    case "question.asked":
      if (!validQuestion(properties) || !addPending(model, model.questions, properties)) model.invalid = true
      break
    case "question.replied":
      if (!isObject(properties) || !validAnswers(properties.answers) ||
        !clearPending(model, model.questions, properties)) model.invalid = true
      break
    case "question.rejected":
      if (!clearPending(model, model.questions, properties)) model.invalid = true
      break
    case "permission.asked":
      if (!validPermission(properties) || !addPending(model, model.permissions, properties)) model.invalid = true
      break
    case "permission.replied":
      if (!isObject(properties) || !["once", "always", "reject"].includes(properties.reply) ||
        !clearPending(model, model.permissions, properties)) model.invalid = true
      break
  }
  if (shouldEmit) await emit(model, publisher)
}

function requestJSON(base, path, directory, authorization, signal) {
  return new Promise((resolve, reject) => {
    let deadline
    let settled = false
    function finish(callback, value) {
      if (settled) return
      settled = true
      if (deadline !== undefined) globalThis.clearTimeout(deadline)
      callback(value)
    }
    const succeed = (value) => finish(resolve, value)
    const fail = (error) => finish(reject, error)

    let url
    try {
      url = new URL(path, base)
      url.searchParams.set("directory", directory)
    } catch (error) {
      fail(error)
      return
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      fail(new Error("unsupported server protocol"))
      return
    }
    const headers = { accept: "application/json" }
    if (authorization) headers.authorization = authorization
    const perform = url.protocol === "https:" ? httpsRequest : httpRequest
    const request = perform(url, { headers, method: "GET", signal }, (response) => {
      const chunks = []
      let size = 0
      response.on("data", (chunk) => {
        size += chunk.length
        if (size > MAX_RESPONSE_BYTES) {
          response.destroy(new Error("response too large"))
          return
        }
        chunks.push(chunk)
      })
      response.on("error", fail)
      response.on("end", () => {
        if (!response.statusCode || response.statusCode < 200 || response.statusCode >= 300) {
          fail(new Error(`HTTP ${response.statusCode || 0}`))
          return
        }
        try {
          succeed(JSON.parse(Buffer.concat(chunks).toString("utf8")))
        } catch (error) {
          fail(error)
        }
      })
    })
    request.on("error", fail)
    request.setTimeout(HTTP_TIMEOUT_MS, () => {
      request.destroy(new Error("OpenCode hydration request timed out"))
    })
    deadline = globalThis.setTimeout(() => {
      const error = new Error("OpenCode hydration request exceeded its absolute deadline")
      request.destroy(error)
      fail(error)
    }, HTTP_TIMEOUT_MS)
    request.end()
  })
}

function snapshotModel(sessions, statuses, questions, permissions) {
  if (!Array.isArray(sessions) || !isObject(statuses) ||
    !Array.isArray(questions) || !Array.isArray(permissions)) return null
  const statusEntries = Object.entries(statuses)
  if (sessions.length > MAX_MODEL_ENTRIES || statusEntries.length > MAX_MODEL_ENTRIES ||
    questions.length > MAX_MODEL_ENTRIES || permissions.length > MAX_MODEL_ENTRIES) return null
  const candidate = newModel()
  for (const value of sessions) {
    if (!validSession(value) || candidate.sessions.has(value.id)) return null
    candidate.sessions.set(value.id, { id: value.id, parentID: value.parentID })
  }
  const graph = sessionGraph(candidate)
  if (graph.unresolved || graph.roots.length === 0) return null
  for (const root of graph.roots) candidate.statuses.set(root, { type: "idle" })
  for (const [sessionID, value] of statusEntries) {
    if (!isIdentifier(sessionID) || !candidate.sessions.has(sessionID) || !validStatus(value)) return null
    candidate.statuses.set(sessionID, { type: value.type })
    if (rootFor(candidate, sessionID) === sessionID && (value.type === "busy" || value.type === "retry")) {
      candidate.armed.add(sessionID)
    }
  }
  for (const value of questions) {
    if (!validQuestion(value) || candidate.questions.has(value.id) ||
      !rootFor(candidate, value.sessionID) ||
      !addPending(candidate, candidate.questions, value)) return null
  }
  for (const value of permissions) {
    if (!validPermission(value) || candidate.permissions.has(value.id) ||
      !rootFor(candidate, value.sessionID) ||
      !addPending(candidate, candidate.permissions, value)) return null
  }
  return candidate
}

function reconcileSnapshotModel(current, candidate) {
  candidate.seenErrors = current.seenErrors
  candidate.order = Math.max(candidate.order, current.order)
  candidate.serial = Math.max(candidate.serial, current.serial)

  function carryTerminal(sessionID, terminal) {
    const previous = candidate.terminal.get(sessionID)
    if (previous && previous.serial >= terminal.serial) return
    candidate.terminal.set(sessionID, terminal.type === "error"
      ? { ...terminal, suppressIdle: false }
      : { ...terminal })
  }

  for (const [sessionID, terminal] of current.terminal) {
    if (rootFor(current, sessionID) !== sessionID ||
      rootFor(candidate, sessionID) !== sessionID ||
      candidate.statuses.get(sessionID)?.type !== "idle") continue
    if (terminal.type === "done" && hasPendingForRoot(candidate, sessionID)) continue
    carryTerminal(sessionID, terminal)
  }

  for (const [sessionID, terminal] of current.pendingErrors) {
    if (rootFor(candidate, sessionID) !== sessionID ||
      candidate.statuses.get(sessionID)?.type !== "idle") continue
    carryTerminal(sessionID, terminal)
  }

  for (const sessionID of current.armed) {
    if (rootFor(current, sessionID) !== sessionID ||
      rootFor(candidate, sessionID) !== sessionID ||
      candidate.statuses.get(sessionID)?.type !== "idle" ||
      hasPendingForRoot(candidate, sessionID) ||
      candidate.terminal.has(sessionID)) continue
    if (candidate.serial >= Number.MAX_SAFE_INTEGER) {
      candidate.invalid = true
      break
    }
    candidate.serial += 1
    candidate.terminal.set(sessionID, {
      identity: `done:${sessionID}:${candidate.serial}`,
      serial: candidate.serial,
      type: "done",
    })
  }
  return candidate
}

export const WispDeck = async (context = {}) => {
  const stateFile = process.env.WISP_DECK_ATTENTION_FILE
  const generation = process.env.WISP_DECK_ATTENTION_GENERATION
  if (!stateFile || !generation) return {}

  const password = process.env.OPENCODE_SERVER_PASSWORD
  const username = process.env.OPENCODE_SERVER_USERNAME || "opencode"
  const authorization = password
    ? `Basic ${Buffer.from(`${username}:${password}`).toString("base64")}`
    : ""
  const directory = typeof context.directory === "string" ? context.directory : ""
  const serverUrl = context.serverUrl
  const publisher = await createPublisher(stateFile, generation)
  if (!publisher) return { event: async () => {} }

  let model = newModel()
  let epoch = 0
  let hydrationAttempt = 0
  let activeHydration = null
  let retryRunning = false
  let retryQueued = false
  let retryStartedEpoch = -1
  let retryLaunchScheduled = false
  let requestedRetryAt = null
  let cleanHydrationRequired = false
  let queue = Promise.resolve()

  function serialize(operation) {
    const result = queue.then(operation, operation)
    queue = result.catch(() => {})
    return result.catch(() => {})
  }

  async function markActiveHydrationDirty() {
    const active = activeHydration
    if (!active || active.settled || active.attempt !== hydrationAttempt) return
    cleanHydrationRequired = true
    model.dirty = true
    await emit(model, publisher)
  }

  async function hydrate(startedAt = epoch) {
    if (!serverUrl || !directory) return
    const attempt = hydrationAttempt + 1
    hydrationAttempt = attempt
    if (activeHydration && !activeHydration.settled) activeHydration.controller.abort()
    const controller = new AbortController()
    const active = { attempt, controller, settled: false, startedAt }
    activeHydration = active
    let values
    let failed = false
    try {
      values = await Promise.all([
        requestJSON(serverUrl, "/session", directory, authorization, controller.signal),
        requestJSON(serverUrl, "/session/status", directory, authorization, controller.signal),
        requestJSON(serverUrl, "/question", directory, authorization, controller.signal),
        requestJSON(serverUrl, "/permission", directory, authorization, controller.signal),
      ])
    } catch {
      failed = true
      controller.abort()
    }
    const candidate = failed ? null : snapshotModel(...values)
    let invalidated = false
    await serialize(async () => {
      if (activeHydration !== active || attempt !== hydrationAttempt) {
        active.settled = true
        return
      }
      active.settled = true
      activeHydration = null
      if (epoch !== startedAt) {
        cleanHydrationRequired = true
        model.dirty = true
        invalidated = true
        await emit(model, publisher)
        return
      }
      cleanHydrationRequired = false
      if (failed || !candidate) {
        model.dirty = false
        model.invalid = true
        await emit(model, publisher)
        return
      }
      model = reconcileSnapshotModel(model, candidate)
      await emit(model, publisher)
    })
    if (invalidated) requestHydrationRetry(epoch)
  }

  function runHydrationRetry() {
    const target = epoch
    if (retryRunning) {
      if (target > retryStartedEpoch) retryQueued = true
      return
    }
    retryRunning = true
    retryStartedEpoch = target
    void (async () => {
      try {
        do {
          retryQueued = false
          await hydrate(retryStartedEpoch)
          retryStartedEpoch = epoch
        } while (retryQueued && (cleanHydrationRequired || chooseState(model).phase === "unknown"))
      } finally {
        const repeat = retryQueued && (cleanHydrationRequired || chooseState(model).phase === "unknown")
        retryRunning = false
        retryQueued = false
        if (repeat) requestHydrationRetry(epoch)
      }
    })()
  }

  function requestHydrationRetry(requestedAt) {
    if (!serverUrl || !directory) return
    if (retryRunning) {
      runHydrationRetry()
      return
    }
    requestedRetryAt = requestedRetryAt === null
      ? requestedAt
      : Math.max(requestedRetryAt, requestedAt)
    if (retryLaunchScheduled) return
    retryLaunchScheduled = true
    void serialize(async () => {
      retryLaunchScheduled = false
      const target = requestedRetryAt
      requestedRetryAt = null
      if (target !== null) runHydrationRetry()
    })
  }

  const hooks = {
    event: async ({ event: value } = {}) => {
      if (!isObject(value) || typeof value.type !== "string") return
      if (!EVENT_TYPES.has(value.type)) return
      let retryAt = null
      if (!isIdentifier(value.id)) {
        await serialize(async () => {
          await markActiveHydrationDirty()
          model.invalid = true
          await emit(model, publisher)
          epoch += 1
          if (chooseState(model).phase === "unknown") retryAt = epoch
        })
        if (retryAt !== null) requestHydrationRetry(retryAt)
        return
      }
      await serialize(async () => {
        await markActiveHydrationDirty()
        await applyEvent(model, publisher, value)
        epoch += 1
        if (chooseState(model).phase === "unknown") retryAt = epoch
      })
      if (retryAt !== null) requestHydrationRetry(retryAt)
    },
  }

  if (serverUrl && directory) void hydrate()
  return hooks
}
