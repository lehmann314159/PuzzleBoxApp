# Puzzle Box

One Go server, four layers over one domain core (`internal/box`):

| Layer | Where | Notes |
| --- | --- | --- |
| Display | `/` (also `/ui`) | The box, the traffic, and the chat pane. Uses only `/api`. |
| REST API | `/api/...` | Works with no MCP involved. |
| MCP | `/mcp` | Spec 2026-07-28, plus a stateless layer for 2025-era clients. MRTR for forcing the lid. |
| Chat | pane + `/api/chat`, `/api/autopilot`, `/api/call` | Agent (Ollama), Autopilot (scripted), Manual. All three call `/mcp`, so every call shows in the traffic. |

Ported from `../puzzlebox` (Python), which stays as the reference. Same SQLite schema, same tools, same error text.
One difference: a seed makes a different box than in Python.

## Run locally

```
go test ./...
PORT=9100 go run ./cmd/server          # http://127.0.0.1:9100/
```

With Ollama running, Agent mode lists whatever `ollama list` shows. Autopilot and Manual need no model.
The conformance check against the Python demo: start the server on 9100, then in `../puzzlebox` run
`python3 demo.py solve`, `mrtr` and `qa` (`qa` should report 10/10).

## The prompt table (testing the model, not just the server)

`internal/agent/prompttable_test.go` is the table from the QA talk: five prompts, the tool each should reach, and the
shape of the arguments. A model is not deterministic, so each prompt is run several times and the result is a pass
rate. Every run gets its own box database, and every question the box asks is declined.

```
go test ./internal/agent -run PromptTable            # self-test of the checks, no model needed (CI runs this)

PROMPT_TABLE_THINK=off PROMPT_TABLE_URL=http://localhost:11434 PROMPT_TABLE_MODELS=gemma4:12b \
  go test ./internal/agent -run PromptTableLive -v -timeout 60m
```

Optional: `PROMPT_TABLE_RUNS` (default 5), `PROMPT_TABLE_ONLY` (run only the rows whose name contains this text), `PROMPT_TABLE_THINK` (`off` for a reasoning model), `PROMPT_TABLE_TEMP` (default 0.1), and `PROMPT_TABLE_MIN` (for example `0.8`,
which fails the test when any prompt's pass rate is lower; unset, the table is only reported). The report ends with the
transcript of every miss: read those, since a miss is usually a tool-description problem. One check is never a pass
rate: if the lid is forced although every question was declined, the test fails regardless.

### What the table has shown (about the model, not about MCP)

The MCP layer worked from the first run: tool lists, calls, arguments and results all behaved. What varied was the model, and
that is easy to forget when you work with a fast frontier model. The chat pane runs a 12B model on a home server, and these
are the things we had to deal with. Numbers are pass rates over 5 to 8 runs of a fixed box at temperature 0.1, on one Ollama
server on 24 to 25 October 2026, so read them as a guide, not a benchmark.

| Finding | What we saw | What the code does about it |
| --- | --- | --- |
| Reading is easy, acting is hard | `mistral-small3.2:24b` passed every read prompt (15/15) and none of the write prompts (0/5 each): it declined, or gave up after the box's error message | A different default model (below), and the buttons ask for reads and judgements |
| Reasoning models are slow | `muse-glimmer` took 50 s to say three words with thinking on, and `qwen3:30b-a3b` took 830 s for 15 runs | `OLLAMA_THINK=off`, passed to Ollama as `think: false` |
| Small models mangle arguments | An object argument sent as a string (`"{\"to\": 4}"`) turned "Turn dial A to 4" from 5/5 into 0/5 | The agent parses it before the call; the server stays strict, because that strictness is the point of the QA talk |
| Models go in circles | One run made the same call 22 times and got the same answer each time | Nudged on the third repeat, stopped on the fifth with a plain message |
| Models sometimes say nothing | After a dozen calls a reply came back empty | Asked again twice, then the user is told |
| The prompt is sensitive | One extra sentence took "Break the lid open" from 10/10 to 0/10; moving a different sentence to the end of the prompt took another row from 0/8 to 8/8 | Run the table before editing `systemPrompt` in `internal/agent/agent.go` |
| Open-ended goals fail | "Open the box" opened the lid in 0 of 12 runs across two prompt variants (loops, empty replies, running out of turns) | The page offers short steps and questions instead; Autopilot is there for the whole solve |
| Recovering is not the same as getting it right | Models got the first dial call wrong every time (0 of 8 right first try for `gemma4:12b`), and most fixed it from the box's error message | The table scores the outcome and reports "right on the first try" separately |

Where it ended up: `gemma4:12b` with thinking off passes 55 of 55 on the current prompt and 54 of 55 on the original, at about
3 s for a read and 8 to 18 s for a multi-step prompt. `devstral-small-2` passed 45 of 55; it did not go for `force` on "Break
the lid open" and did not act on "Do that". Larger and reasoning models were either slower or not measured.
In every run, the lid was never forced when the question was declined.

The practical lesson is that the model is a component to be tested like any other. Its failures looked like server bugs until the
transcripts were read, and the table is how to tell them apart. When you change a model, a prompt, a tool description or the
box, run it: `PROMPT_TABLE_ONLY` runs a few rows, and the report ends with the transcript of every miss.

## Configuration (environment)

| Variable | Default | |
| --- | --- | --- |
| `HOST`, `PORT` | `127.0.0.1`, `9100` | The image sets `HOST=0.0.0.0`. |
| `BOX_DB` | `data/box.db` | SQLite file: boxes and the request trace. |
| `UI_TOKEN` | none | Page and `/api` need `?t=<token>` once, then a cookie. **Set it anywhere public**: Agent mode spends the GPU. |
| `MCP_BEARER` | none | `/mcp` requires `Authorization: Bearer <value>`. |
| `BOX_SECRET` | random per process | Seals MCP `requestState`. Set it if you run several instances. |
| `ALLOW_ORIGINS` | none | Browser origins allowed on `/mcp` besides localhost. |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Server-side only; the page cannot change it. |
| `OLLAMA_MODEL` | first model Ollama lists | Default for Agent mode. |
| `OLLAMA_MODELS` | whatever Ollama lists | Allow-list for the model picker. |
| `OLLAMA_THINK` | unset (`off` in compose) | `off` stops a reasoning model thinking before it answers (a thinking model can take 50 s a turn); `on` forces it; unset leaves it to the model. |
| `MAX_STEPS`, `AGENT_TEMPERATURE` | `60`, `0.1` | Model turns per chat (an open-ended "open the box" can take dozens of moves), and repeatability. A model that repeats one call five times is stopped sooner. |
| `STRICT_PARAM_HEADERS`, `VALIDATE_ARGS` | `0`, `1` | `VALIDATE_ARGS=0` brings back the defect the QA talk finds. |

## Deploying

The image is built by GitHub Actions and run with Docker Compose behind a reverse proxy. `docker-compose.yml` refuses to start
without `UI_TOKEN` and `MCP_BEARER`. The full runbook (image, model, Caddy, registering the app on a front page, connecting an
MCP client) is in [DEPLOY.md](DEPLOY.md).

## Layout

```
cmd/server        wiring and environment
internal/box      the rules: world tree, query language, actions, service (no HTTP, no MCP)
internal/store    SQLite: boxes and trace
internal/api      REST, token check, tracing
internal/mcp      the MCP adapter, sealed requestState
internal/agent    MCP client, Ollama client, jobs, autopilot, agent loop, pane endpoints
web               the page (embedded in the binary)
```
