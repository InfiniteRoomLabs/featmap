package main

// Guard against a class of bug that took down the entire MCP tools/list in
// development: a tool whose emitted outputSchema contains a *boolean subschema*.
//
// JSON Schema 2020-12 permits `true`/`false` as full schemas, and the go-sdk
// emits `"<field>": true` for a struct field of type `any`. But the Claude Code
// MCP client's schema validator rejects a boolean where it expects a schema
// object ("Invalid input"), and that one rejection aborts the WHOLE tools/list
// -- every tool disappears, not just the offender. The fix for such a tool is to
// give the handler an `any` RETURN type (so the sdk emits no outputSchema at
// all), not a typed wrapper with an `any` field.
//
// This test connects an in-memory client to the real server (buildMCPServer),
// lists the tools exactly as a client would, and fails if any tool's
// outputSchema carries a boolean in a subschema position. It needs no database,
// so it runs in CI's DB-free lint pass (name matches the ^TestMCP filter).

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPOutputSchemasAreClientSafe(t *testing.T) {
	ctx := context.Background()

	server := buildMCPServer()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "schema-guard", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools listed")
	}

	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("%s: marshal outputSchema: %v", tool.Name, err)
		}
		var schema any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: unmarshal outputSchema: %v", tool.Name, err)
		}
		if path := findBoolSubschema(schema, "$"); path != "" {
			t.Errorf("tool %q emits a boolean subschema at %s -- strict MCP clients reject "+
				"this and drop the ENTIRE tools/list. Give the handler an `any` return type "+
				"(no outputSchema) instead of a typed wrapper with an `any` field.", tool.Name, path)
		}
	}
}

// TestMCPFindBoolSubschema proves the detector above is not vacuous: it must
// flag a boolean in a subschema position and must NOT flag legal boolean
// keywords. Without this, TestMCPOutputSchemasAreClientSafe could pass simply
// because the walker never detects anything.
func TestMCPFindBoolSubschema(t *testing.T) {
	mustUnmarshal := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return v
	}

	// The exact shape the go-sdk emits for an `any` field -- must be flagged.
	dirty := mustUnmarshal(`{"type":"object","properties":{"results":true},"required":["results"],"additionalProperties":false}`)
	if got := findBoolSubschema(dirty, "$"); got != "$.properties.results" {
		t.Fatalf("expected detection at $.properties.results, got %q", got)
	}

	// A boolean nested in items must also be flagged.
	dirtyItems := mustUnmarshal(`{"type":"object","properties":{"xs":{"type":"array","items":true}}}`)
	if got := findBoolSubschema(dirtyItems, "$"); got != "$.properties.xs.items" {
		t.Fatalf("expected detection at $.properties.xs.items, got %q", got)
	}

	// Legal boolean KEYWORDS (additionalProperties:false, required[]) must NOT trip it.
	clean := mustUnmarshal(`{"type":"object","properties":{"feature":{"type":"object","additionalProperties":false}},"required":["feature"],"additionalProperties":false}`)
	if got := findBoolSubschema(clean, "$"); got != "" {
		t.Fatalf("expected clean schema, got detection at %q", got)
	}
}

// findBoolSubschema walks a JSON-Schema document and returns the path to the
// first boolean found in a SUBSCHEMA position: a value under "properties", or an
// "items" value. Boolean *keywords* such as "additionalProperties": false or
// "required" entries are legal and deliberately ignored. Returns "" if clean.
func findBoolSubschema(node any, path string) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, v := range props {
			child := path + ".properties." + name
			if _, isBool := v.(bool); isBool {
				return child
			}
			if p := findBoolSubschema(v, child); p != "" {
				return p
			}
		}
	}
	if items, ok := m["items"]; ok {
		child := path + ".items"
		if _, isBool := items.(bool); isBool {
			return child
		}
		if p := findBoolSubschema(items, child); p != "" {
			return p
		}
	}
	return ""
}
