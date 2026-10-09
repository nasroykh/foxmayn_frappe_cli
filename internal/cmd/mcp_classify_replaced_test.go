package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// A tool replaced on the embedded server is not ffc's handler: Classify
// reports an error (the replacement may have run) and survives a panic.
func TestClassifyReplacedTool(t *testing.T) {
	s, _ := mcpTConfirmServer(t, "", MCPOptions{})
	args := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	tool := s.ListTools()["get_doc"].Tool

	ran := false
	s.AddTool(tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ran = true
		return mcp.NewToolResultText("x"), nil
	})
	if _, err := s.Classify("get_doc", args); err == nil || !strings.Contains(err.Error(), "own handler") {
		t.Errorf("replaced handler: %v", err)
	}
	if !ran {
		t.Error("the replacement was expected to run (documented)")
	}

	s.AddTool(tool, server.ToolHandlerFunc(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("boom")
	}))
	if _, err := s.Classify("get_doc", args); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("panicking handler: %v", err)
	}
}
