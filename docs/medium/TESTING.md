# Testing an MCP server with an agent in front of it

*This copy is formatted for Medium: tables are lists, diagrams and code samples are described in text, and sub-headings are bold lines. The repository has the original, with tables: https://github.com/lehmann314159/PuzzleBoxApp. File paths in the text refer to that repository.*

Adding MCP to a system adds a layer where errors can happen. Putting an agent in front of the MCP server adds another, and the
new layer is not deterministic. This document lists what went wrong while building and testing the Puzzle Box, what found each
problem, and the practice that follows. It is written from this project's evidence, and each point says where the evidence is.
Everything here comes from one small application, built and tested by one person working with Claude. It is a starting point and a
set of things to try in your own system, not findings that have been shown to generalise. Section 4 lists what is still open.

Related: `docs/HOW-IT-WORKS.md` (how the server works), `docs/verify.sh` (the protocol checks), `docs/STRANGER-TEST.md` (the
brief and questions for first-time-visitor runs), and the README's section on the prompt table.

## 1. Where things can fail

A request from a person passes through these layers. Each one failed at least once during this project, and the check that
caught it was different each time.

- **The domain (rules of the box).** What can go wrong: A rule is wrong or a message misleads. What caught it here: `go test`; a tester reading the latch's reply.
- **The MCP adapter.** What can go wrong: A header, version, error code or pause-and-resume step departs from the spec. What caught it here: `docs/verify.sh`; reading the spec pages.
- **The tool surface (names, descriptions, schemas, error text).** What can go wrong: A model picks the wrong tool, sends the wrong arguments or cannot recover from an error. What caught it here: The live prompt table.
- **The client or agent loop.** What can go wrong: Arguments arrive mangled, the model repeats itself, goes beyond what was asked, or reports what it did not do. What caught it here: The prompt table; transcripts; the traffic panel.
- **The model.** What can go wrong: The same prompt gives different answers on different runs. What caught it here: Repeating each prompt and reporting a pass rate.
- **The page a person sees.** What can go wrong: Something needed is hidden, unclickable or unexplained. What caught it here: Strangers using the page, and the page's own traffic panel.
- **Deployment.** What can go wrong: A proxy, token or client setting blocks a real client. What caught it here: Connecting a real client to the deployed server.

The practical consequence is that no single suite covers this. In this project `go test` passed throughout a change that took three
rows of the prompt table from 5/5 to 0/5, because `go test` does not call a model.

## 2. Principles

Each principle is something the project showed, with the evidence.

**2.1 Test each layer where it can fail, and know what each suite cannot see**

- `go test ./...` checks the domain and the adapters. It cannot see the model.
- `docs/verify.sh` starts real servers and checks protocol behaviour over HTTP: handshake-free requests, header mismatches, the
  confirmation round trip across instances, forged and replayed state. It cannot see the model either.
- The prompt table is the only suite that calls a model. It needs an Ollama server and takes about nine minutes for one model.
- Strangers, using the deployed page, are the only test of whether a person can find their way.

Evidence: on 2026-10-25 a block added to the system prompt left `go test` green and took "What can I do to the lid?", "Is the
lid open, and where are the dials?" and "Do that" to 0/5 (`docs/evidence/prompt-table-1-with-prompt-block.log`).

**2.2 Treat prompts, tool descriptions and schemas as code, and gate them on the live table**

A sentence in the system prompt, a line in a tool description or an example in a schema changes what the model does. The README
records a sentence that took one row from 10/10 to 0/10, and the regression above is the second time. Run the table before
pushing such a change, compare it with the last table, and do not push if a row drops.

Evidence: three runs, in `docs/evidence/`. With the prompt block: three rows at 0/5. With the block reverted and a dial-turn
example kept in the `act` description: the dials row still missed (50/55 in total). With both reverted: 55/55 again. The cause was
found by removing one change at a time, which needs the table to be fast enough to rerun.

**2.3 Repeat every prompt, fix the conditions, and report a pass rate**

A model is not deterministic, so one pass proves little. The table runs each prompt five times at temperature 0.1 with thinking
off, and reports a rate per row. Things that stayed fixed: the model, the temperature, the box (a fixed seed), and the checks.
Report two numbers where they differ. For "Turn dial A to 4" the outcome was 5/5 while the first call was right 0/5 times, and
the difference is the part that tells you about the tool description.

**2.4 Read the transcripts of the misses**

A miss is a question about the tool surface, and often not about the server. The table ends with the transcript of every miss
for that reason. In the regression above, the transcripts showed the two read rows failing because the model called `open_box`
first, and "Do that" failing because the model examined and repeated its suggestion. Neither would be visible in a pass rate.

**2.5 Check the checks**

A failing check is a question about either side. In this project the checks themselves were wrong several times:
- The prompt table once scored correct answers as misses because it compared how a target was spelled (`dial.A`) instead of what
  it meant (README).
- The first draft of `docs/verify.sh` assumed that the box saying no was an error result. The latch's reply is `isError: false`.
  The check was wrong, and writing it down corrected the paper.
- The script broke on macOS's bash 3.2, which mishandles braces inside nested command substitutions. It looked like 24 failures in
  the server and was one problem in the test.

Before treating a failure as a defect, work out whether the check is right.

**2.6 Test the negative cases, and keep each one**

One early negative case found a defect in the server: a call with no verb gave an unhelpful message and still cost a move.
That defect needed a negative case to find, and the case stays as a regression test (`ValidateArgs`, in `Service.Act`). The
suite has since added negative cases for header mismatch, an unknown tool, forged and tampered state,
state for another box, and a missing `Mcp-Param-BoxId`. When a new defect turns up, add the check that would have caught it.

**2.7 Check against the spec text, not against memory**

Reading the 2026-07-28 pages found two departures that no test had: the server accepted a missing `Mcp-Param-BoxId`, which the
spec says to reject, and the confirmation token could be replayed within its lifetime. The first is now fixed and the second is
documented, and both are covered by checks. The spec's own examples are worth turning into checks directly.

**2.8 Test across instances, not only against one**

If the protocol layer is meant to be stateless, show that. `docs/verify.sh` mints a confirmation token on one instance and
redeems it on a second that shares the secret, and shows a third instance with a different secret refusing it. A test against a
single process would have passed even if the server kept state in memory.

**2.9 Do not trust the model's account of what it did**

The model's description of its own actions is a claim to test, not data. Examples from this project:
- It said "I have now opened the lid" after only examining it, and reported listen results at three positions after listening once.
- A model driving Claude Code concluded that the server's confirmation did not exist ("the description promises something that
  doesn't exist"). The client had handled the confirmation round trip itself, so the model never saw it. The traffic row showed
  `inputResponses` with `action: accept`.

Compare what the model says with the box's state and with the wire. The first-time-visitor tester caught the overclaiming by
comparing the chat with the status chips and the traffic panel.

**2.10 Make the wire visible, and make it easy to find**

The traffic panel records every request with its headers, client name and full bodies, and it sits next to the chat. It turned out
to be the most useful test instrument in the project: it exposed the chat's overclaiming, showed that Claude Code sends
`Mcp-Param-BoxId` and uses 2026-07-28. Two cautions from the same panel:
- It showed only the tab's own box, so a second client's traffic was invisible until "follow latest" was ticked. Tooling that
  filters can hide the evidence you need, and the filter has to be findable.
- A server-side prompt and a client-side prompt can look alike to a person. Whether the "accept or decline" dialog was the
  server's confirmation or Claude Code's own permission prompt could only be settled from the wire.

**2.11 Use strangers, with the conditions written down**

Two fresh Claude sessions played the live page as first-time visitors, each given the same kind of brief: a task, a ban on reading
files or calling the API, a friction log and follow-up questions.
- Run 1 (told it could click): opened the lid but never reached the drawer, because nothing on the box could be clicked to get there,
  and the key to the sounds was only in truncated text. Each is now fixed.
- Run 2 (told to use the chat only): solved the box in 12 messages with no clicks on the box, rated clarity 4/5, and found the chat's
  overclaiming.

Practices that follow:
- **A new session each time.** A model cannot be told to forget, and a tester that has seen the page carries it over.
- **Separate runs for separate paths.** Run 1 used the box and run 2 used the chat; one run cannot test both.
- **Questions that ask for evidence.** Ask the tester to quote the exact text that told them something, and allow "I didn't".
  Otherwise a tester will reconstruct a tidy story.
- **Some friction belongs to the tester's tools.** Stale screenshots, an accessibility tree that lacked the box's parts, and
  truncated hint text appeared in the friction log. Separate what the page did from what the tool did.

**2.12 Test with a real third-party client, and treat the client as a variable**

Claude Code connected to the deployed server, used 2026-07-28, sent `Mcp-Param-BoxId` and completed the confirmation round trip.
Claude desktop's connector form has no field for a header, so it could not send this server's static bearer token and was not
tried. The client determines what is possible. A client can handle protocol steps the model never sees, and a client can be
unable to authenticate at all.

**2.13 Fix problems at the layer where they are cheapest and most reliable**

Several of the model's problems were handled in code, not in wording:
- Arguments sent as a JSON string are repaired in the agent before the call, while the server stays strict (`asObjects`).
- A change to the box is allowed only on a part the user named (`intentOf` and `refusal`).
- A repeated call is nudged on the third time and stopped on the fifth.
- Each model call has a time limit, because about one in 190 never answered.

Prompt wording was the least reliable fix, as the regression showed. When the model overclaimed, the fix that was tried at the prompt
level was reverted. The chosen response was to leave it and rely on the wire being visible.

**2.14 Decide what is acceptable, and write it down**

The chat's overclaiming was not fixed. The decision, for a demo, was to accept it because the traffic panel and the status chips
let a person check. That is a legitimate outcome of testing. What matters is that it is a recorded decision with a reason, and not a
defect nobody looked at.

**2.15 Record the evidence and say what was not checked**

Each claim in `docs/HOW-IT-WORKS.md` says how it was checked, and the sections on departures and unchecked items are as long as they
need to be. Logs from the three prompt-table runs are in `docs/evidence/`. Commits are cited. What is untested is stated: other
clients, claude.ai connectors, other models in this round, load, and anything beyond two instances.

## 3. A working procedure

What to run, by what changed. These are the checks this project used, in the order they were useful.

- **A rule in the box.** Run: `go test ./...`. Pass means: All pass.
- **The MCP adapter, headers, versions, error codes, the confirmation flow.** Run: `go test ./...` and `bash docs/verify.sh`; reread the spec page for the area. Pass means: All pass; no departure that is not listed in the paper.
- **A tool name, description or schema; the system prompt; the model; the temperature.** Run: The live prompt table (below), then compare with the last table. Pass means: No row falls; read every miss.
- **Page text, clickable areas, the opening message.** Run: A stranger run in a fresh session, one for each path (box and chat) Pass means: The stranger reaches the goal without asking; friction items are triaged.
- **A server change that is about to be redeployed.** Run: `go test`, `verify.sh`, the table. Pass means: All three clean.
- **After a redeploy.** Run: Open the page; connect a real client; tick "follow latest" and read a row. Pass means: The page loads; the client connects; the headers and version are as expected.
- **A model or client claims it did something.** Run: Compare with the box's state and the traffic row. Pass means: The claim matches the evidence, or the mismatch is recorded.

The prompt table, for one model:

`PROMPT_TABLE_THINK=off PROMPT_TABLE_URL=<ollama url> PROMPT_TABLE_MODELS=gemma4:12b go test ./internal/agent -run PromptTableLive -v -timeout 40m`

When a change makes a row fall:
1. Read the transcript of the miss.
2. Remove one change at a time and rerun until the cause is clear.
3. Revert the change that did it, or rework it and rerun.
4. Record what happened, as `docs/evidence/` does for 2026-10-25.

## 4. Open spaces

What this project did not do, and questions it leaves open. Treat the principles above as hypotheses until your own system
confirms or contradicts them.

**Not done**

- Only one model (`gemma4:12b`) was run in this round of testing. The README records an earlier comparison with others.
- One temperature, one seed, five runs per row. The rates are a guide.
- No load or concurrency test beyond two instances sharing a database.
- No automated stranger run. The strangers were Claude sessions started by hand, from the brief in `docs/STRANGER-TEST.md`.
- No test with a client other than this repository's own and Claude Code.
- The MRTR field shapes were compared with the spec page by reading, not by a schema validator.

**Open questions**

- **Scale.** The server has four tools. How do tool-selection accuracy and the cost of the table change with dozens of tools, and
  how should the table be organised when there are too many rows to run on every change?
- **Other models.** The findings come mostly from a 12B model on a home server. A larger or hosted model may make several of the
  failures here disappear, and may introduce different ones. Which of these principles survive a better model is not known.
- **The generic-tool argument is reasoned, not measured.** `docs/HOW-IT-WORKS.md` argues that one tool per verb would remove some
  failure classes. Nobody built that version and ran the same table against it.
- **Open-ended goals.** "Open the box" in one prompt succeeded in 2 of 23 runs in the README's earlier measurement. The table tests
  short, fixed prompts well. How to test long, goal-directed behaviour, and what a pass rate means for it, is unsettled.
- **Judging strangers.** The tester sessions were read by hand. Whether a stranger run can be automated and scored reliably, and
  whether a model tester's friction log matches what a human would report, was not tested.
- **Cost and cadence.** The table needs a GPU and about nine minutes for one model. Whether that belongs in CI, on a schedule or
  before each release is a decision this project made informally, by running it when prompts changed.
- **Authentication.** The server takes a static bearer token, which one common client cannot send. How to test the OAuth
  paths that MCP supports, and what a server like this should offer, is untried.
- **Many clients.** Two clients were tried. Whether header mirroring, confirmation and the compatibility layer hold up across
  more is unknown.
- **Non-determinism in the protocol layer.** The adapter was tested deterministically. Whether an agent's retries, timeouts and
  duplicate calls expose races in a stateless server (the confirmation token is replayable) beyond what the two-instance check
  covers is untested.

## 5. The shortest version

1. Test every layer, and know what each test cannot see.
2. Prompts, descriptions and schemas are code. Gate them on the live table.
3. Repeat runs, fix the conditions, read the misses.
4. A failing check may be the wrong check.
5. Keep a regression test for each defect and each negative case.
6. Check against the spec, across instances, and with a real client.
7. A model's account of itself is a claim. Check it against the wire.
8. Make the wire visible and easy to find.
9. Use strangers, in fresh sessions, with questions that ask for evidence.
10. Fix problems in code where you can, and write down what you chose not to fix.

