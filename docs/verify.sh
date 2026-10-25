#!/usr/bin/env bash
# Checks the behaviour that docs/HOW-IT-WORKS.md describes, against real servers started by this script.
# Needs: go, curl, python3. Uses ports 9291-9295 and a temporary directory; touches nothing else.
set -u
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'pkill -f "$T/pb" 2>/dev/null; rm -rf "$T"' EXIT
go build -o "$T/pb" ./cmd/server || exit 1

serve() { # port instance db secret [bearer]
  PORT=$1 INSTANCE=$2 BOX_DB=$T/$3.db BOX_SECRET=$4 MCP_BEARER=${5:-} "$T/pb" >"$T/$2.log" 2>&1 &
}
up() { for _ in $(seq 50); do curl -s -o /dev/null "localhost:$1/healthz" && return; sleep 0.2; done; echo "server on $1 did not start"; cat "$T"/*.log; exit 1; }
# Started one at a time: two processes creating the same SQLite file at once can collide.
serve 9291 one a s3cret; up 9291
serve 9292 two a s3cret; up 9292   # same database, same secret: a second instance of one service
serve 9293 three b other; up 9293  # different database and secret
serve 9294 locked c s3cret tok; up 9294
STRICT_PARAM_HEADERS=0 serve 9295 lenient a s3cret; up 9295  # the escape hatch

pass=0; fail=0
check() { # description, expected substring, actual
  if [[ "$3" == *"$2"* ]]; then pass=$((pass+1)); echo "ok    $1"; else fail=$((fail+1)); echo "FAIL  $1"; echo "      want: $2"; echo "      got:  ${3:0:300}"; fi
}

VER=2026-07-28
meta() { echo "\"_meta\":{\"io.modelcontextprotocol/protocolVersion\":\"$VER\",\"io.modelcontextprotocol/clientCapabilities\":$1}"; }
body() { echo "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":{$2}}"; }  # method params-json
status() { local port=$1; shift; curl -s -o /dev/null -w '%{http_code}' -XPOST "localhost:$port/mcp" "$@"; }
rpc() { # port method params-json [extra curl args...]  (modern headers; Mcp-Name taken from params)
  local port=$1 method=$2 params=$3; shift 3
  local name; name=$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]).get("name",""))' "{$params}")
  local bid; bid=$(python3 -c 'import json,sys;print(json.loads(sys.argv[1]).get("arguments",{}).get("box_id",""))' "{$params}")
  curl -s -XPOST "localhost:$port/mcp" -H "MCP-Protocol-Version: $VER" -H "Mcp-Method: $method" ${name:+-H "Mcp-Name: $name"} \
    ${bid:+-H "Mcp-Param-BoxId: $bid"} "$@" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":{$params}}"
}
tool() { # port name args-json caps-json [extra-params]
  rpc "$1" tools/call "\"name\":\"$2\",\"arguments\":$3,$(meta "$4")${5:+,$5}"
}
field() { python3 -c 'import json,sys;d=json.load(sys.stdin);r=d.get("result",d);print(eval(sys.argv[1]))' "$1"; }

ASK='{"elicitation":{}}'; NOASK='{}'

echo "== stateless: no handshake, version and capabilities in every request"
check "server/discover works with no initialize"        '"supportedVersions"' "$(rpc 9291 server/discover "$(meta "$NOASK")")"
check "initialize is refused on the modern path"        'initialize is not part of' "$(rpc 9291 initialize "$(meta "$NOASK")")"
check "a 2025 initialize still works (compat layer)"    '"protocolVersion":"2025-11-25"' "$(curl -s -XPOST localhost:9291/mcp -H 'MCP-Protocol-Version: 2025-06-18' -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}')"
check "unsupported version lists what is supported"     '"supported":["2026-07-28"' "$(curl -s -XPOST localhost:9291/mcp -H 'MCP-Protocol-Version: 1999-01-01' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')"
check "GET /mcp is 405"                                 '405' "$(curl -s -o /dev/null -w '%{http_code}' localhost:9291/mcp)"
check "results carry resultType, ttlMs and cacheScope"  'ttlMs' "$(rpc 9291 tools/list "$(meta "$NOASK")")"

echo "== headers mirror the body"
LISTBODY=$(body tools/list "$(meta "$NOASK")")
check "Mcp-Method must match the body"                  'Header mismatch: Mcp-Method' "$(curl -s -XPOST localhost:9291/mcp -H "MCP-Protocol-Version: $VER" -H 'Mcp-Method: tools/call' -d "$LISTBODY")"
BOX=$(tool 9291 open_box '{"seed":"verify"}' "$NOASK" | field 'r["structuredContent"]["box_id"]')
PARAMS="\"name\":\"examine\",\"arguments\":{\"box_id\":\"$BOX\"},$(meta "$NOASK")"
PBODY=$(body tools/call "$PARAMS")
PH=(-H "MCP-Protocol-Version: $VER" -H 'Mcp-Method: tools/call' -H 'Mcp-Name: examine')
check "Mcp-Param-BoxId, when sent, must match box_id"   'Mcp-Param-BoxId does not match' "$(curl -s -XPOST localhost:9291/mcp "${PH[@]}" -H 'Mcp-Param-BoxId: box_other' -d "$PBODY")"
check "Mcp-Param-BoxId, when omitted, is refused by default" 'Mcp-Param-BoxId is required' "$(curl -s -XPOST localhost:9291/mcp "${PH[@]}" -d "$PBODY")"
check "STRICT_PARAM_HEADERS=0 accepts the omission"     '"isError":false' "$(curl -s -XPOST localhost:9295/mcp "${PH[@]}" -d "$PBODY")"

echo "== MRTR: the human gate on forcing the lid"
FORCE="{\"box_id\":\"$BOX\",\"target\":\"lid\",\"verb\":\"force\"}"
FORCEC="{\"box_id\":\"$BOX\",\"target\":\"lid\",\"verb\":\"force\",\"args\":{\"confirm\":true}}"
FIRST=$(tool 9291 act "$FORCE" "$ASK")
check "a client that can ask gets input_required"       '"resultType":"input_required"' "$FIRST"
check "confirm:true does not bypass a client that can ask" '"resultType":"input_required"' "$(tool 9291 act "$FORCEC" "$ASK")"
check "a client that cannot ask is told how to confirm" 'cannot ask' "$(tool 9291 act "$FORCE" "$NOASK")"
check "a client that cannot ask may confirm in args"    'The lid shudders' "$(tool 9291 act "$FORCEC" "$NOASK")"
STATE=$(echo "$FIRST" | field 'r["requestState"]')
ANS='"inputResponses":{"confirm_force":{"action":"accept","content":{"proceed":true}}}'
check "state minted on one instance is accepted by another sharing the secret" 'The lid shudders' "$(tool 9292 act "$FORCE" "$ASK" "\"requestState\":\"$STATE\",$ANS")"
check "the same state is refused where the secret differs" 'expired or does not belong' "$(tool 9293 act "$FORCE" "$ASK" "\"requestState\":\"$STATE\",$ANS")"
check "the same state works a second time: it is not single-use" 'The lid shudders' "$(tool 9292 act "$FORCE" "$ASK" "\"requestState\":\"$STATE\",$ANS")"
check "a tampered state is refused"                     'expired or does not belong' "$(tool 9291 act "$FORCE" "$ASK" "\"requestState\":\"${STATE%?}x\",$ANS")"
OTHER=$(tool 9291 open_box '{"seed":"other"}' "$NOASK" | field 'r["structuredContent"]["box_id"]')
FORCE_OTHER="{\"box_id\":\"$OTHER\",\"target\":\"lid\",\"verb\":\"force\"}"
check "state for one box does not unlock another"       'expired or does not belong' "$(tool 9291 act "$FORCE_OTHER" "$ASK" "\"requestState\":\"$STATE\",$ANS")"

echo "== errors written for a reader that has to recover"
NOVERB="{\"box_id\":\"$BOX\",\"target\":\"lid\"}"
check "missing verb: names the fix, costs nothing"      "Use examine on 'lid'" "$(tool 9291 act "$NOVERB" "$NOASK")"
NOPATH="{\"box_id\":\"$BOX\",\"path\":\"box.nothing\"}"
check "unknown path: lists what is there"               'Here you can see' "$(tool 9291 examine "$NOPATH" "$NOASK")"
check "unknown box: says how to get one"                'Use open_box' "$(tool 9291 examine '{"box_id":"box_0000000000","path":"box"}' "$NOASK")"
check "an unknown tool is a protocol error, not a tool result" '"code":-32602' "$(tool 9291 nope '{}' "$NOASK")"
RELEASE="{\"box_id\":\"$BOX\",\"target\":\"latch\",\"verb\":\"release\"}"
check "the box saying no in the story is not an error"  '"isError":false' "$(tool 9291 act "$RELEASE" "$NOASK")"

echo "== edges"
DISC=$(body server/discover "$(meta "$NOASK")")
H=(-H "MCP-Protocol-Version: $VER" -H 'Mcp-Method: server/discover')
check "no bearer: 401"        '401' "$(status 9294 "${H[@]}" -d "$DISC")"
check "right bearer: 200"     '200' "$(status 9294 "${H[@]}" -H 'Authorization: Bearer tok' -d "$DISC")"
check "foreign Origin: 403"   '403' "$(status 9291 "${H[@]}" -H 'Origin: http://evil.example' -d "$DISC")"
check "localhost Origin: 200" '200' "$(status 9291 "${H[@]}" -H 'Origin: http://localhost:3000' -d "$DISC")"

echo; echo "$pass passed, $fail failed"; [ "$fail" -eq 0 ]
