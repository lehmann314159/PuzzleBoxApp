# How the Puzzle Box works: a technical introduction to MCP, through one small server

*This copy is formatted for Medium: tables are lists, and sub-headings are bold lines. The repository has the original, with tables: https://github.com/lehmann314159/PuzzleBoxApp. File paths in the text refer to that repository.*

This is a tour of the MCP side of a small Go application, written for the revision of the spec dated 2026-07-28. The
application is about 3,000 lines of Go without tests, plus a 650-line web page. The MCP server itself is about 450 of those lines
(`internal/mcp`). The rest is the puzzle (`internal/box`, about 780), a REST API (265), storage (165), wiring (90) and the chat
and agent clients (about 1,250).
It is meant for someone who has used MCP through a client and now wants to know what a server actually does. Everything
here is about this code. Where a claim is about the protocol, it says which part of the spec it comes from and whether it was
checked. `docs/verify.sh` runs the behavioural claims against real servers (28 checks, a few seconds once built).

Spec pages used, all read on 2026-10-25: the [changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog),
[Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http.md) and
[multi round-trip requests](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr.md).

Two clients have been used against it: this repo's own (the agent pane, Autopilot and manual caller) and Claude Code, which
connected to the deployed server, called its tools and completed the confirmation round trip in section 5. Its traffic row
shows client name `claude-code`, protocol `2026-07-28` and an `Mcp-Param-BoxId` header on a call that carries a `box_id`.

## Try it

- **The code:** https://github.com/lehmann314159/PuzzleBoxApp. The README explains how to run it locally. `docs/verify.sh`
  checks this paper's behavioural claims against servers it starts itself.
- **A running copy:** https://puzzlebox.verynormalserver.com/?t=1ab6d2f37822aa58bd28dd88e287905e36d574c428ed99e9. It is a
  brass box on a table, a click menu for each part, a chat that can operate the box, and a panel showing every request as it
  goes over the wire. Tick "follow latest" to see traffic from other clients, such as Claude Code.

The running copy is a home server with one shared GPU, and it may be offline. The chat uses that GPU, so please keep long runs of
Autopilot or chat to a minimum. The link carries a shared access token; if it is misused, the token will be changed and the link
will stop working.

The MCP endpoint of the running copy needs a bearer token that is not published here. To connect an MCP client, run your own copy
(`go run ./cmd/server`; without `MCP_BEARER` set, `/mcp` needs no token) and add it with
`claude mcp add --transport http puzzlebox http://127.0.0.1:9100/mcp`.

## 1. The shape of the program

```
                        +-------------------+
 browser page  ---REST--->  internal/api    |
 (box + traffic)        |                   |
                        |   box.Service     |---> store (SQLite: boxes, trace)
 any MCP client --/mcp-->  internal/mcp    |
                        |                   |
 agent pane / Autopilot / manual  --/mcp--->|   (the app's own clients call its own /mcp)
                        +-------------------+
```

- `internal/box` is the domain: what a box is, what each part does, what the sounds mean. It knows nothing about HTTP or MCP.
  Its public face is `box.Service` with four operations, `Open`, `Examine`, `Query`, `Act` (`internal/box/service.go`).
- `internal/api` is a REST adapter over `Service`. The page uses it to draw the box and to run the click menus.
- `internal/mcp` is an MCP adapter over the same `Service`. Each tool is a few lines that unpack arguments, call the service and
  wrap the answer (`internal/mcp/call.go`).
- `internal/agent` contains three clients (a scripted solver, a manual tool caller and a model loop). **All three call this
  server's own `/mcp` over HTTP**, with a client name each (`agent-pane`, `autopilot`, `manual`). That is deliberate: every step
  they take goes through the same door an outside client uses, so the traffic panel shows it exactly as it would show yours.

Most of the code is not MCP. The MCP adapter is about 450 lines, and most of those are
protocol housekeeping (headers, versions, the pause-and-resume for confirmation). The behaviour a model sees, such as what
`examine` returns and what an error says, lives in the domain and is shared with the REST API.

## 2. One request, on the wire

There is no handshake and no session. Every request carries what the server needs. This is `open_box` as this server sees it
(real, captured from a local run; the `_meta` keys are the ones the 2026-07-28 changelog names):

```
POST /mcp
MCP-Protocol-Version: 2026-07-28
Mcp-Method: tools/call
Mcp-Name: open_box

{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{
  "name":"open_box","arguments":{"seed":"doc"},
  "_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",
           "io.modelcontextprotocol/clientCapabilities":{}}}}
```

```
200 OK
{"jsonrpc":"2.0","id":2,"result":{
  "resultType":"complete",
  "content":[{"type":"text","text":"A brass box is on the table: box_139d544b82. It is silent."}],
  "structuredContent":{"box_id":"box_139d544b82"},
  "isError":false,
  "_meta":{"io.modelcontextprotocol/serverInfo":{"name":"puzzlebox","version":"1.0.0","instance":"go1"}}}}
```

What to notice, with the code that does it (`internal/mcp/mcp.go` unless stated):

- **The server never initializes anything.** There is no `initialize` on the modern path: sending one gets a 404 and
  `Method not found: initialize is not part of 2026-07-28` (`dispatch`). A client can call `server/discover` first, which
  returns supported versions, capabilities, instructions and a cache hint, but nothing requires it. *Verified by script;
  matches the changelog items "remove the initialize/notifications/initialized handshake" and "servers MUST implement
  server/discover".*
- **Version and capabilities travel in `_meta`** and are mirrored into the `MCP-Protocol-Version` header. The server reads the
  client's capabilities from this request, not from a stored session (`handle`: `caps` comes from `meta`).
- **Every result says `resultType`.** `complete` for an ordinary answer and `input_required` for a paused one (section 5).
  *Matches changelog item 8.*
- **List results say how long they stay good.** `tools/list` returns `ttlMs: 300000` and `cacheScope: "public"`;
  `server/discover` returns `ttlMs: 3600000`. *Matches changelog minor change 5, "Require ttlMs and cacheScope".*
- **Notifications get `202` and no body; `GET /mcp` gets `405`** with `Allow: POST`; an `Mcp-Session-Id` header is never read.
  *Matches the transport page ("respond with 405 Method Not Allowed", "ignore it, and do not mint or echo session IDs").*
- **A legacy client still works.** `initialize` from a 2025-era client returns a 2025 answer. The compatibility layer is small
  (a few branches in `dispatch`) because the server holds no per-connection state in either era. It always answers with the
  newest legacy version, `2025-11-25`, whichever legacy version the client asked for. That is a simplification, not
  negotiation.
- **The transport headers must agree with the body.** `Mcp-Method` must equal the body's `method`; `Mcp-Name` must equal
  `params.name`; the version header must equal `_meta`. A mismatch is `400` with JSON-RPC error `-32020`
  (`validateModern`). The reason the spec gives, and the reason it matters here, is that a gateway can route or block on a
  header while the server acts on the body, and the two must not be able to disagree. *Verified; code `-32020` and status
  `400` match the transport page.*
- **Origin and bearer checks come first** (`post`): a foreign `Origin` gets `403`; with `MCP_BEARER` set, a missing or wrong
  token gets `401` with `WWW-Authenticate: Bearer`. A request with no `Origin` header is accepted. *The spec requires a 403
  for a present, invalid `Origin`; verified.*

## 3. Four generic tools

`internal/mcp/tools.go` defines `open_box`, `examine`, `query` and `act`. Nothing in the names says puzzle, lid or combination:

- **`open_box(seed?, reset?)`**: Put a box on the table, get a `box_id` handle (creates).
- **`examine(box_id, path)`**: What is at this path, what it contains, which verbs it accepts (reads).
- **`query(box_id, select)`**: Many reads at once, GraphQL-shaped: `{ lid { open } dials { position } }`; `{ __fields }` lists what exists (reads).
- **`act(box_id, target, verb, args?)`**: Do something to a part (changes).

Notes on the design:

- **The descriptions are the interface a model reads.** The model never sees the Go code, only the tool descriptions, the
  schema, and what comes back. Everything it learns about the box it learns by calling `examine`. The verbs for each part
  (`turn`, `listen`, `release`, `open`, `force`, `pull`, `speak`) are returned as data in the `actions` field of `examine`, not
  written in the tool descriptions.
- **`box_id` is annotated `"x-mcp-header": "BoxId"`.** The spec lets a server say that a tool argument should also appear as a
  header named `Mcp-Param-{name}`, so that a gateway can route on it without parsing the body. `validateModern` checks it
  matches when present. See section 7, deviation 1.
- **Results carry both text and structure.** `content` holds the sentence a model reads; `structuredContent` holds the
  fields a program wants (`box_id`, `moves`). The text is written for the model: "Turn it how? Give {to: 0-9} or {by:
  integer}."

**Why `act` is generic, and what that costs**

`act(box_id, target, verb, args?)` is one tool for every change: turning a dial, releasing the latch, lifting the lid, pulling the
drawer, speaking a word, forcing the lid. That was a choice, made so the box would behave like a service the model has not been
told about. The model has to ask what each part accepts, as it would with an API nobody documented. It also makes a good place to
look at a tension that real servers face, because the alternative is easy to picture: `turn_dial`, `release_latch`, `open_lid`,
`force_lid`, `pull_drawer` and `speak`.

- **Tool list.** Generic `act`: Four tools, unchanged when the box gains a part. Per-verb tools: Grows with the service; every change is a new tool.
- **What the model knows before it acts.** Generic `act`: Almost nothing; it must call `examine` first. Per-verb tools: The schema says what each tool takes.
- **Argument checking.** Generic `act`: At runtime, by the server, in prose. Per-verb tools: By the schema, before the call.
- **Describing risk.** Generic `act`: One annotation for everything `act` can do. Per-verb tools: One annotation per tool.
- **Policy by tool name.** Generic `act`: All changes or none. Per-verb tools: Per action.
- **Discovery.** Generic `act`: Part of the interface. Per-verb tools: Replaced by the tool list.

What the generic shape cost in this app:

- **Argument errors, found only at runtime.** The schema for `act` says `args` is an object. It cannot say that `turn` on a dial
  needs `to`, an integer from 0 to 9. The README's prompt table records the first dial call wrong in 0 of 8 runs for
  `gemma4:12b`, with most runs recovering from the box's error message. A `turn_dial` tool with a required `to` would have
  made that class of failure impossible, not just recoverable.
- **Risk that cannot be described truthfully.** `act` is annotated `destructiveHint: false`, and it can also force the lid.
  An annotation on a tool is one claim about everything the tool can do, so for `act` no value is correct. A client that
  decides whether to ask a person from the annotation sees a harmless tool. Here the server's own confirmation (section 5)
  covers `force`, which is why the gap did no harm, but the annotation still misdescribes it.
- **Policy at the wrong grain.** A gateway can allow or block a call using only the `Mcp-Name` header (section 2). With a
  generic `act` it can block every change or none. To block only `force` it would have to read the body, which is what the
  headers were meant to avoid. A separate `force_lid` tool could be blocked on its own. The same grain applies on the client
  side. Claude Code's permission rules for an MCP tool name either a whole server (`mcp__puzzlebox`) or one tool
  (`mcp__puzzlebox__act`), and its documentation says a settings-file rule for an MCP tool cannot carry a parameter: such a rule
  is skipped, and matching on a parameter is possible only for a deny rule passed on the command line. So a standing approval
  for `act` covers `force` as well, and a settings file cannot say "allow `act` but not with verb `force`". (Source: Claude Code's
  permissions page, read 2026-10-25. The page does not say what the "don't ask again" option stores for an MCP call, and it does
  not mention tool annotations such as `destructiveHint`, so how a client weighs the annotation is not established here.) In our
  test the server's own confirmation fired for `force` anyway, which is the control that does not depend on the client's
  settings.
- **A defect class that only exists because of the shape.** An early negative test found a defect in this server: a call with
  no verb returned an unhelpful message and still cost a move. `verb` is a free string, so omitting it is possible. With one tool
  per verb there is no verb to omit. The fix (`ValidateArgs`, in `Service.Act`) is a runtime check standing in for what a schema
  would have done.

**What it means for QA.** The tension is most visible when you test. With specific tools, a lot of the checking moves into the
schema and is done before the server sees the call; what is left to test is mostly behaviour. With a generic tool, the schema
accepts nearly anything, so correctness of the *arguments* becomes something to test, and the subject of the test is partly the
model. The prompt table in the README exists for this reason: it checks that a prompt led to the right tool, and also to the
right target, verb and arguments, and it reports a pass rate because the model is not deterministic. The error messages become part
of the interface and need tests of their own; the ones in the next subsection are checked by `docs/verify.sh`. The generic shape also makes
the tool descriptions carry more weight: a sentence added to one moved results in the live table (see the 2026-10-25
regression in section 6). A server with specific tools would have fewer of these failure modes to test, and more tools to
describe well.

**A middle path.** Keep the reads generic (`examine` and `query` are already read-only and annotated as such), and split changes
by risk: `act` for ordinary moves and a separate tool for the one destructive move. The model keeps discovery for the common case.
Annotations, policy and permission prompts get something accurate to work with for the dangerous case. Whether that is worth the
extra tool depends on how much the server cares about those three, and this demo cares more about the discovery.

This is a comparison from one small app, not a rule. The point is that "four generic tools" is a position on a tradeoff, and the
costs above are what the position cost here.

**Two kinds of "no"**

This distinction affects how well a model recovers from its own mistakes.

- **The box's story says no.** How the server says it: A normal result, `isError: false`, text in the story's voice. Example: `The latch holds. The wheels inside are not aligned. Listen at the grille to hear how close each one is.`.
- **The caller misused the interface.** How the server says it: A normal result with `isError: true`, and text that says what to do instead. Example: `act needs a verb. Use examine on 'lid' to see which verbs it accepts.`; `nothing at 'box.nothing'. Here you can see: ...`; `There is no box 'box_0000000000' on the table. Use open_box to get one.`.
- **The request is not valid MCP.** How the server says it: A JSON-RPC error, no tool result. Example: unknown tool: code `-32602`, HTTP 200; header mismatch: code `-32020`, HTTP 400; unknown method on the modern path: code `-32601`, HTTP 404.

The first two are both results a model receives, and a model can read them. The third kind is a protocol-level failure, and
a model is, in general, less likely to recover from one of those than from a readable result (a common observation; this project did
not measure it). Putting misuse in the second row
and listing the valid choices in the text keeps the model in the conversation. *Verified by script, including that the
"story says no" case is `isError: false` and an unknown tool is a JSON-RPC error.*

One check on "costs nothing": a call with no verb is rejected before the box is touched and does not use a move
(`Service.Act`, `ValidateArgs`). A dial turn with the wrong arguments does use a move (`Apply` increments moves first). That is
a known roughness, found by a tester who saw the counter rise after failed calls.

## 4. State: a handle, not a session

The 2026-07-28 changelog says it directly: "Servers that need cross-call state use explicit, server-minted handles passed as
ordinary tool arguments." That is how this server works.

- **`box_id` is the whole state.** `open_box` makes one. `BoxID(seed)` is `box_` plus the first ten hex characters of
  SHA-256 of the seed, so a given seed always gives the same handle; with no seed, four random bytes are used.
- **State is in SQLite, keyed by that handle** (`internal/store/store.go`): a `boxes` table of JSON documents and a `trace` table
  of every request. Nothing about a box lives in the process.
- **Writes are atomic.** `Store.Update` loads, mutates and saves inside one transaction, and the connection string sets
  `_txlock=immediate`, so two requests that change the same box queue up instead of overwriting each other.
- **So any instance can serve any request.** The script starts two servers on one database file and shows that a paused
  request begun on one is finished on the other (section 5). Nothing in the protocol layer would have to change to put a load
  balancer in front of several copies. *Verified by script with two instances sharing a database and a secret.*
- **The puzzle's answer is derived, not stored separately.** `Secrets(seed)` computes the combination, the star count and the
  word from the seed with a seeded generator, so the same seed always gives the same box. The seed itself sits in the stored
  state, and the page's "reveal secrets" box shows the derived values. So "the answer is written down nowhere the model can read"
  is true of what the tools return, not of the database. With a random seed, which is four bytes, the answer is also not
  strong against someone who has the handle and time. This is a demo, not a vault.

## 5. A human in the loop: multi round-trip requests

`act` with verb `force` on the lid is the risky move. It damages the box and costs extra moves, so it needs a person's yes.
This is where MCP's 2026-07-28 shape is most visibly different from earlier revisions, and `internal/mcp/call.go` (`act`) is
the whole implementation.

```
client  --> tools/call act {lid, force}
server  <-- resultType: "input_required"
            inputRequests: { confirm_force: { method: "elicitation/create",
                              params: { mode: "form", message: "Forcing the lid may damage the box ... Do it anyway?",
                                        requestedSchema: { proceed: boolean } } } }
            requestState: "<opaque token>"
            (the server has forgotten the request; nothing is waiting)
client  asks the person, then:
client  --> tools/call act {lid, force}   (the original call again, plus:)
            inputResponses: { confirm_force: { action: "accept", content: { proceed: true } } }
            requestState: "<the same token, unchanged>"
server  <-- the lid shudders ... (an ordinary result)
```

- **No connection is held open and the server remembers nothing.** In earlier revisions a server asked the client a question
  over a stream and waited. Here the question is the *answer* to the first call, and the client re-sends the original call with
  the reply attached. *Matches changelog item 7.*
- **`requestState` is how the server remembers anyway.** It is a token the server mints and the client carries back. Here it is
  base64 of `{box, target, verb, expiry}`, a dot, and an HMAC-SHA256 over it (`internal/mcp/seal.go`). The secret is `BOX_SECRET`.
  Anything forged, tampered with, expired (five minutes), or issued for a different box, target or verb is refused with
  "That confirmation expired or does not belong to this request." *Verified: a state minted by one instance is accepted by a
  second that shares the secret, and refused by a third with a different secret; a tampered token and a token for another box
  are both refused.* The server is stateless in the sense that matters: any instance with the secret can finish the request.
  The token's contents are readable (it is signed, not encrypted), so it must not carry anything secret.
- **The client must be able to ask.** The server looks at the client capabilities in the same request. A client that declares
  `elicitation` gets the question. A client that does not is told, in text, `Forcing the lid needs confirmation, and this client
  cannot ask. Retry with args {confirm: true} if you really mean it.` Only such a client may confirm by passing `confirm: true`.
  A client that can ask is never allowed to skip the question that way. *Verified in both directions.* Before the check existed, a model could
  pass `confirm: true` itself and walk through the gate meant for a human.
- **A second client did it too.** Claude Code, against the deployed server, received `input_required`, put the question to its
  user, and retried `act` with `inputResponses: {confirm_force: {action: "accept", content: {proceed: true}}}` and the
  `requestState` unchanged. The model behind Claude Code never saw the round trip; from its side the call simply returned the
  lid's answer. A model that is not told this may conclude, wrongly, that the box had no gate.
- **The agent pane asks a real person.** `Manager.callTool` in `internal/agent/manager.go` turns an `input_required` result into a
  prompt on the page, then retries with the person's `accept` or `decline` and the state copied unchanged, up to three rounds.
  The REST API has the same gate in simpler form: `409` with `needs_confirmation`, and the page asks.

**Compared with the spec's MRTR page.** The `input_required` result (`resultType`, `inputRequests` keyed by a
server-chosen name, an `elicitation/create` entry with `mode`, `message` and `requestedSchema`, and `requestState`), the client's
`inputResponses` (`action` and `content`) and the rule that the client echoes `requestState` unchanged all match the page. So does
the rule that a server must not ask for elicitation from a client that has not declared it, and that `requestState` must be
integrity protected and treated as attacker-controlled. Two things the page says a server *should* do are not done here:

- **No principal in the state.** The page suggests binding `requestState` to the authenticated principal. This server has one
  shared bearer token and no notion of who is calling, so there is nothing to bind to. The state is bound to the box, target and
  verb, and expires after five minutes. (The page suggests a digest of the request's salient parameters; the box, target and verb
  stand in for that.)
- **The state is not single-use.** The same token can be presented again within its five minutes and will force the lid again,
  without a new answer from the person. The page notes that these measures do not give single use and that a server that needs it
  must enforce it. For a demo box the cost is extra damage to a lid. A server that redeems something once would need a
  server-side record, and that record is the shared state MRTR was designed to avoid. *Verified by script.*

## 6. The model in the loop

The chat pane shows a different set of problems. A server can be correct and still have a poor conversation with a model. The
README has the detailed findings (a table of what a 12B model got wrong and what the code does about each), and `docs/TESTING.md`
turns them into principles and a procedure; the short version, for someone who writes servers:

- **Be strict on the server and forgiving in the client.** Small models send an object argument as a JSON string
  (`"{\"to\": 4}"`). The server rejects it and stays strict, because relaxing the server would hide a real bug from every other
  client. The agent repairs it before the call (`asObjects`, `internal/agent/agent.go`).
- **Guard intent in the client, not in the prompt.** The model once followed "Turn dial A to 4" by also turning dial B.
  A move that changes the box is now allowed only on a part the user's message names (`intentOf` and `refusal`,
  `internal/agent/intent.go`); anything else comes back as "You were not asked to change the dial B." This is cheaper and
  more reliable than asking the model to behave.
- **Write errors for a reader that has to recover.** "act needs a verb. Use examine on 'lid' to see which verbs it accepts" gets a
  model back on track; "Nothing happens" does not.
- **Prompt wording is fragile and the only test of it is the model.** On 2026-10-25 a block of sentences added to the system
  prompt (to stop the model saying it had done things it had only examined) took three rows of the live prompt table from 5/5
  to 0/5 against `gemma4:12b`; `go test` passed throughout, because it never calls the model. The block was reverted and the
  table returned to 55/55 (commits `627b1c9` and `e1bb5ba`). Run the table before changing a prompt or a tool description.
- **Show the evidence next to the chat.** The traffic panel shows every call the chat made, with arguments and results. In a
  first-time-visitor test, a tester caught the chat saying "I have now opened the lid" after it had only examined it, because the
  traffic and the status chips disagreed. The trace let the tester check the model's claims against what had actually run.

## 7. What is not in this server, and where it departs from the spec

This server implements the parts of the 2026-07-28 spec that its four tools need. What follows is what it leaves out or does
differently.

1. **`Mcp-Param-BoxId` is required, as the spec says.** The spec says a server `MUST` reject a request that omits a recognised
   `Mcp-Param-*` header whose value is in the body. This server rejected a *wrong* value from the start but, until 2026-10-25,
   accepted a *missing* header by default. Both clients tried send the header (Claude Code's traffic row shows
   `Mcp-Param-Boxid`, and this repo's own client sets it on every call), so the default is now strict. Setting
   `STRICT_PARAM_HEADERS=0` restores the lenient behaviour for a client that does not mirror headers. *Verified by script,
   including the escape hatch, and by running Autopilot end to end against a server with the default configuration (solved in
   18 moves).*
2. **Base64-encoded header values are not decoded.** The spec defines an `=?base64?...?=` form for header values that are not
   plain ASCII. This server compares header values literally. It does not matter for `box_id` (always `box_` and hex) or the
   tool names here, but a server with other names would need it.
3. **No `subscriptions/listen`.** The server declares only `tools` with no `listChanged`, so it promises no change
   notifications and the tool list never changes. A server whose tools change would need the stream.
4. **No `MissingRequiredClientCapability` error.** When a client cannot elicit, the server returns a normal result that tells
   the model how to proceed. That was chosen so the model can read it and act; a client that wants the typed error will not find it.
5. **`ping` is not available on the modern path** (answer: 404 `Method not found`), which matches the changelog's removal of
   `ping`. It still works for legacy clients.
6. **Error codes** `-32000` (bad Origin) and `-32001` (bearer) are in the range the spec leaves to implementations.
   `-32020`, `-32022` and `-32601` follow the spec.
7. **Tools only.** No resources, prompts, sampling, roots or tasks. The server needs none of them, so these are untested here.
8. **Clients tried:** this repo's own client and Claude Code. Claude desktop's custom
   connector form has no field for a header, so it cannot send the static bearer token this server requires; it was not tried.
   Hosting behind a path-prefix proxy was not tested.

## 8. Reproducing and extending this

- `bash docs/verify.sh` builds the server, starts five instances on ports 9291-9295 with temporary databases and runs 28 checks
  against them. Needs `go`, `curl` and `python3`. It prints each check and exits non-zero if any fail. Run it before
  changing anything in `internal/mcp`.
- `go test ./...` covers the domain and the adapters. It does not call a model.
- The live prompt table (see the README) is the only test that does, and it needs an Ollama server.
- To add a tool: add its schema in `internal/mcp/tools.go`, a case in `callTool` in `internal/mcp/call.go`, and put the behaviour in
  `box.Service`, so the REST API can offer it as well. Write the description and the error messages as if for a reader with no
  other documentation, because for the model that is exactly the situation.

## 9. What was checked for this document

- **No handshake; `initialize` refused on the modern path; `server/discover` works alone:** `docs/verify.sh`; changelog items 2 and 3
- **`resultType`, `ttlMs`, `cacheScope` on results:** script; changelog item 8 and minor change 5
- **202 / 405 / ignored session header:** script (405); transport page; the 202 path is read from the code only
- **Header mismatch is 400 with `-32020`; unknown version is 400 with a `supported` list:** script; transport page
- **Origin 403; bearer 401:** script; transport page for Origin
- **`isError` semantics (story no = false, misuse = true, unknown tool = RPC error):** script
- **MRTR: `input_required`, state round trip, cross-instance resume, forgery, wrong box:** script; changelog item 7
- **`confirm: true` cannot skip the question for a client that can ask:** script (with the app's own client only)
- **MRTR field shapes and requestState rules:** read against the spec's MRTR page, item by item; the two gaps above
- **The state is not single-use:** script
- **Claude Code connects, uses 2026-07-28, sends `Mcp-Param-BoxId`, and completes the MRTR round trip:** its traffic row on the deployed server (client `claude-code`, `inputResponses` and `requestState` present in the retry), 2026-10-25
- **Departures from the spec in section 7:** read from code and the transport page; item 1 also by script, and by an Autopilot run against the default configuration
- **The 55/55 table and the 2026-10-25 regression:** `docs/evidence/` logs; commits `627b1c9`, `e1bb5ba`

