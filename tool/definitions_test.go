package tool

import "testing"

// TestDefaultToolDefinitions 特征化 DefaultToolDefinitions：17 个工具、名称唯一且非空。
func TestDefaultToolDefinitions(t *testing.T) {
	defs := DefaultToolDefinitions()

	want := map[string]bool{
		"read_file": true, "write_file": true, "edit_file": true, "list_dir": true,
		"file_search": true, "grep_search": true, "get_errors": true, "run_command": true,
		"get_symbols": true, "lsp_code_action": true, "todo": true, "delegate_task": true,
		"askQuestions": true, "memory": true, "newWorkspace": true, "resolveMemoryFileUri": true,
		"run_skill": true,
	}
	if len(defs) != len(want) {
		t.Fatalf("definitions count = %d, want %d", len(defs), len(want))
	}

	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		if d.Name == "" {
			t.Errorf("tool with empty name")
			continue
		}
		if seen[d.Name] {
			t.Errorf("duplicate tool name: %s", d.Name)
		}
		seen[d.Name] = true
		if !want[d.Name] {
			t.Errorf("unexpected tool: %s", d.Name)
		}
		if d.Description == "" {
			t.Errorf("tool %s has empty description", d.Name)
		}
	}
}
