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

Optional: `PROMPT_TABLE_RUNS` (default 5), `PROMPT_TABLE_TEMP` (default 0.1), and `PROMPT_TABLE_MIN` (for example `0.8`,
which fails the test when any prompt's pass rate is lower; unset, the table is only reported). The report ends with the
transcript of every miss: read those, since a miss is usually a tool-description problem. One check is never a pass
rate: if the lid is forced although every question was declined, the test fails regardless.

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
| `MAX_STEPS`, `AGENT_TEMPERATURE` | `30`, `0.1` | Safety net and repeatability. |
| `STRICT_PARAM_HEADERS`, `VALIDATE_ARGS` | `0`, `1` | `VALIDATE_ARGS=0` brings back the defect the QA talk finds. |

## Deploy on the gx10

This follows `frontPage/ADDING_A_SERVICE.md`. Port **9100**, subdomain **puzzlebox.verynormalserver.com**.

1. **Image.** Push to GitHub; `.github/workflows/docker-publish.yml` tests, then builds `lehmann314159/puzzlebox`
   for amd64 and arm64 (needs the `DOCKERHUB_TOKEN` secret, as in the other repos).
2. **Model.** On the gx10: `ollama pull gemma4:12b`. The container reaches the host's Ollama through
   `host-gateway:11434`, the same way Terve does, so Ollama must listen beyond localhost (`OLLAMA_HOST=0.0.0.0`).
3. **Compose.** Copy `docker-compose.yml` and `.env.example` to `~/apps/puzzlebox/`, then:
   ```
   cp .env.example .env     # fill in UI_TOKEN, MCP_BEARER, BOX_SECRET with: openssl rand -hex 24
   docker compose pull && docker compose up -d
   ```
   Compose refuses to start without `UI_TOKEN` and `MCP_BEARER`.
4. **Caddy on the gx10**, in `/etc/caddy/Caddyfile`, then `sudo systemctl reload caddy`:
   ```
   @puzzlebox host puzzlebox.verynormalserver.com
   handle @puzzlebox {
       reverse_proxy localhost:9100
   }
   ```
5. **Caddy on the Lightsail instance**, then `sudo systemctl reload caddy` (without this TLS fails):
   ```
   puzzlebox.verynormalserver.com {
       reverse_proxy http://<gx10-host>:18080 {
           header_up Host {host}
           transport http {
               dial_timeout 10s
               response_header_timeout 300s
           }
       }
   }
   ```
   `response_header_timeout` matters here: an Agent turn can take a while on a 24B model. Also add the subdomain to the
   Lightsail load balancer's TLS certificate (last item of the ADDING_A_SERVICE checklist).
6. **frontPage.** Add the app at `https://verynormalserver.com/admin/apps` (Apps are database rows, not config):
   - Name: `Puzzle Box`
   - URL: `https://puzzlebox.verynormalserver.com/?t=<UI_TOKEN>` (the token in the link is what lets visitors in without admin;
     leave it off if you would rather hand the token out separately)
   - Description: `A brass box, an MCP server, and a chat that can open it. Watch the protocol as it happens.`
   - Icon: `🧰`
   - Source URL: the GitHub repo.

Connect an MCP client:
`claude mcp add --transport http puzzlebox https://puzzlebox.verynormalserver.com/mcp --header "Authorization: Bearer <MCP_BEARER>"`

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
