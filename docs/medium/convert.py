# Regenerates docs/medium/*.md from docs/*.md. Run from the repository root: python3 docs/medium/convert.py
import re, sys

AS_LISTS = {
    'POST /mcp\nMCP-Protocol-Version': [
        "The request:",
        "",
        "- `POST /mcp`",
        "- Header `MCP-Protocol-Version: 2026-07-28`",
        "- Header `Mcp-Method: tools/call`",
        "- Header `Mcp-Name: open_box`",
        "- Body: a JSON-RPC request with `id` 2 and `method` `tools/call`. Its `params` are `name` `open_box`, `arguments` `{\"seed\": \"doc\"}`, "
        "and a `_meta` object holding `io.modelcontextprotocol/protocolVersion` set to `2026-07-28` and "
        "`io.modelcontextprotocol/clientCapabilities` set to `{}`.",
        "",
    ],
    '200 OK\n{"jsonrpc"': [
        "The response:",
        "",
        "- Status `200 OK`",
        "- Body: a JSON-RPC result with `id` 2 and `resultType` `complete`",
        "- `content`: one text block, \"A brass box is on the table: box_139d544b82. It is silent.\"",
        "- `structuredContent`: `{\"box_id\": \"box_139d544b82\"}`",
        "- `isError`: `false`",
        "- `_meta`: the server's identity under `io.modelcontextprotocol/serverInfo` (name `puzzlebox`, version `1.0.0`, instance `go1`)",
        "",
    ],
    'PROMPT_TABLE_THINK=off': [
        "`PROMPT_TABLE_THINK=off PROMPT_TABLE_URL=<ollama url> PROMPT_TABLE_MODELS=gemma4:12b go test ./internal/agent -run PromptTableLive -v -timeout 40m`",
    ],
    'browser page  ---REST--->': [
        "The pieces fit together like this:",
        "",
        "- The browser page talks REST to `internal/api`. It draws the box, runs the click menus and shows the traffic.",
        "- Any MCP client talks to `/mcp`, which is `internal/mcp`.",
        "- The agent pane, Autopilot and the manual caller are also MCP clients: they call this server's own `/mcp`.",
        "- `internal/api` and `internal/mcp` both call `box.Service`, which reads and writes the store (SQLite: the boxes and the trace).",
        "",
        "The packages:",
        "",
    ],
    'client  --> tools/call act {lid, force}': [
        "The exchange has five steps:",
        "",
        "1. The client sends `tools/call` for `act` with target `lid` and verb `force`.",
        "2. The server answers with `resultType: \"input_required\"`. The answer has an `inputRequests` entry named `confirm_force`: an "
        "`elicitation/create` form whose message says \"Forcing the lid may damage the box ... Do it anyway?\" and asks for a boolean "
        "`proceed`. It also has a `requestState`, an opaque token. The server has now forgotten the request, and nothing is waiting.",
        "3. The client asks the person.",
        "4. The client sends the original call again, adding `inputResponses` with `confirm_force: {action: \"accept\", content: {proceed: true}}` "
        "and the same `requestState`, unchanged.",
        "5. The server returns an ordinary result: the lid shudders.",
        "",
        "What to notice:",
        "",
    ],
}

def cells(line):
    line = line.strip()
    if line.startswith('|'): line = line[1:]
    if line.endswith('|'): line = line[:-1]
    return [c.strip() for c in re.split(r'(?<!\\)\|', line)]

def end(s):
    s = s.strip()
    return s if s and s[-1] in '.?!:)' else s + '.'

RENAME = {'One generic `act`': 'Generic `act`', 'One tool per verb': 'Per-verb tools'}

def table_to_bullets(rows):
    head = [RENAME.get(h.strip(), h.strip()) for h in cells(rows[0])]
    out = []
    if head[0] == 'Tool':  # the tool list reads better as a sentence than as labelled fields
        for r in rows[2:]:
            c = cells(r)
            out.append(f"- **{c[0].strip('*')}**: {end(c[1]).rstrip('.')} ({c[2]}).")
        return out
    for r in rows[2:]:
        c = cells(r)
        if len(head) == 2:
            first = c[0].strip('*')
            out.append(f"- **{first}:** {c[1]}")
        else:
            parts = [f"{head[j].strip()}: {end(c[j])}" if head[j].strip() else end(c[j]) for j in range(1, len(c))]
            first = c[0].strip('*')
            out.append(f"- **{first}.** " + " ".join(parts))
    return out

def convert(src, note):
    lines = open(src).read().split('\n')
    out, i, fence = [], 0, False
    while i < len(lines):
        l = lines[i]
        if l.startswith('```') and not fence:
            j = i + 1
            while not lines[j].startswith('```'): j += 1
            block = '\n'.join(lines[i + 1:j])
            key = next((k for k in AS_LISTS if k in block), None)
            if key:  # ASCII diagrams do not survive Medium's importer; say the same thing as a list
                out += AS_LISTS[key]
            else:
                out += lines[i:j + 1]
            i = j + 1; continue
        if l.startswith('|'):
            j = i
            while j < len(lines) and lines[j].startswith('|'): j += 1
            out += table_to_bullets(lines[i:j]); i = j; continue
        m = re.match(r'^### (.*)$', l)
        if m:
            out.append(f"**{m.group(1)}**"); i += 1; continue
        l = re.sub(r'<(https?://[^>]+)>', r'\1', l)
        out.append(l); i += 1
    text = '\n'.join(out)
    # insert note after the title
    first_nl = text.index('\n')
    return text[:first_nl] + '\n\n' + note + text[first_nl:]

note = ("*This copy is formatted for Medium: tables are lists, diagrams and code samples are described in text, and sub-headings are bold lines. The repository has the original, "
        "with tables: https://github.com/lehmann314159/PuzzleBoxApp. File paths in the text refer to that repository.*")
for name in ['HOW-IT-WORKS.md', 'TESTING.md']:
    open(f'docs/medium/{name}', 'w').write(convert(f'docs/{name}', note) + '\n')
