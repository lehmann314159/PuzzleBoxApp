package mcp

var boxIDSchema = map[string]any{"type": "string", "description": "Handle returned by open_box", "x-mcp-header": "BoxId"}

// Tools returns the tool definitions, for callers that only need to show them.
func Tools() []map[string]any { return tools }

// tools are deliberately generic, so a model has to discover the service rather than be told.
var tools = []map[string]any{
	{"name": "open_box",
		"description": "Put a puzzle box on the table and get its box_id handle; every other tool needs " +
			"that handle. The same seed always gives the same box. reset=true starts an " +
			"existing box over. Boxes are kept until the server database is cleared.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"seed": map[string]any{"type": "string"}, "reset": map[string]any{"type": "boolean"}}}},
	{"name": "examine",
		"description": "Look closely at one part of a box. path is dotted, e.g. 'box', 'dials.A', 'lid'. " +
			"Returns what you can see there, what it contains, and which actions it accepts.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"box_id": boxIDSchema, "path": map[string]any{"type": "string", "default": "box"}},
			"required": []string{"box_id"}},
		"annotations": map[string]any{"readOnlyHint": true}},
	{"name": "query",
		"description": "Read many things at once using a GraphQL-style selection, e.g. " +
			"'{ lid { open } dials { id position } }'. Unknown fields come back as errors that " +
			"list the valid ones; '__fields' lists every field on a node. Read-only.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"box_id": boxIDSchema, "select": map[string]any{"type": "string"}},
			"required": []string{"box_id", "select"}},
		"annotations": map[string]any{"readOnlyHint": true}},
	{"name": "act",
		"description": "Do something to a part of the box: target, verb, optional args. The verbs each part " +
			"accepts are shown by examine (or the 'actions' field in query). Changes the box. " +
			"Example: target 'dials.A', verb 'turn', args {\"to\": 4}.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"box_id": boxIDSchema, "target": map[string]any{"type": "string"},
			"verb": map[string]any{"type": "string"}, "args": map[string]any{"type": "object"}},
			"required": []string{"box_id", "target", "verb"}},
		"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false}},
}
