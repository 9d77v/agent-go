// Package tool provides shared tool system types and definitions for agent-go consumers.
package tool

// DefaultToolDefinitions returns a shared set of 18 tool definitions
// in ToolDefinition format (framework-neutral, no application-specific names).
// These are the standard tools available to all agent-go based applications.
// Application-specific tools (e.g., logo generator, FFmpeg) should be defined
// in the application layer, not here.
func DefaultToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read file contents with optional line range. Returns text content, total lines, and file size.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":      map[string]any{"type": "string", "description": "File path (absolute or relative to workspace root)."},
					"startLine": map[string]any{"type": "integer", "description": "Optional start line (1-based)."},
					"endLine":   map[string]any{"type": "integer", "description": "Optional end line (inclusive)."},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write content to a file. Creates directories if needed. Backs up existing files automatically.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "File path to write."},
					"content": map[string]any{"type": "string", "description": "File content to write."},
				},
				"required": []any{"path", "content"},
			},
		},
		{
			Name:        "edit_file",
			Description: "Perform inline find-and-replace edits on a file. Multiple edits are applied sequentially. Each oldText must match exactly in the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "File path to edit."},
					"edits": map[string]any{
						"type":        "array",
						"description": "List of edits to apply sequentially.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"oldText": map[string]any{"type": "string", "description": "Exact text to find (include surrounding context for uniqueness)."},
								"newText": map[string]any{"type": "string", "description": "Replacement text."},
							},
							"required": []any{"oldText", "newText"},
						},
					},
				},
				"required": []any{"path", "edits"},
			},
		},
		{
			Name:        "list_dir",
			Description: "List directory contents. Returns file/folder names, types, and sizes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Directory path (absolute or relative to workspace root)."},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "file_search",
			Description: "Search files by glob pattern in the workspace. Returns matching file paths.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"glob": map[string]any{"type": "string", "description": "Glob pattern, e.g. '*.go', 'src/**/*.ts'."},
				},
				"required": []any{"glob"},
			},
		},
		{
			Name:        "grep_search",
			Description: "Search file contents with text or regex pattern. Returns matching file paths, line numbers, and content.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":          map[string]any{"type": "string", "description": "Text or regex pattern to search."},
					"isRegexp":       map[string]any{"type": "boolean", "description": "Whether query is a regex pattern."},
					"includePattern": map[string]any{"type": "string", "description": "Optional file glob filter, e.g. '*.go'."},
				},
				"required": []any{"query"},
			},
		},
		{
			Name:        "get_errors",
			Description: "Get compile/lint errors for files or the whole workspace. Returns error locations and messages.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filePaths": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional file paths to check. If empty, checks the entire workspace.",
					},
				},
			},
		},
		{
			Name:        "run_command",
			Description: "Execute a shell command in the workspace directory. Commands are classified by risk level. Dangerous commands require user approval.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":     map[string]any{"type": "string", "description": "Shell command to execute."},
					"explanation": map[string]any{"type": "string", "description": "Optional explanation of what the command does."},
					"goal":        map[string]any{"type": "string", "description": "Optional goal or purpose of the command."},
				},
				"required": []any{"command"},
			},
		},
		{
			Name:        "get_symbols",
			Description: "Query code symbols (functions, types, variables) via language server. Returns symbol names, types, and locations.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Symbol name or keyword to search for."},
				},
				"required": []any{"query"},
			},
		},
		{
			Name:        "lsp_code_action",
			Description: "Apply code actions from the language server at a specific file location.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filePath": map[string]any{"type": "string", "description": "File path."},
					"line":     map[string]any{"type": "integer", "description": "Line number (0-based)."},
					"column":   map[string]any{"type": "integer", "description": "Column number (0-based)."},
				},
				"required": []any{"filePath", "line"},
			},
		},
		{
			Name:        "todo",
			Description: "Manage a structured todo list for the current session. Pass the ENTIRE list of todo items each call — the tool replaces the whole list (full-list replacement). The id field is optional and server-assigned: omit it when creating items; the server auto-increments integer ids and echoes them in the tool result so later calls can reference them to update statuses in place. Only title is required (status defaults to not-started). Use for multi-step tasks; a new task starts with a fresh list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"todoList": map[string]any{
						"type":        "array",
						"description": "Array of todo items. Each item has title (required) and optional status; id is server-generated — integers or numeric strings are accepted, anything else is ignored and a new id is assigned.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":     map[string]any{"type": "integer", "description": "Server-assigned auto-increment id (integer). Optional — omit when creating items; include to update the status of an existing item. Non-integer values are ignored and a new id is assigned."},
								"title":  map[string]any{"type": "string", "description": "Todo item title (required)."},
								"status": map[string]any{"type": "string", "enum": []any{"not-started", "in-progress", "completed"}, "description": "Optional. Defaults to not-started."},
							},
							"required": []any{"title"},
						},
					},
				},
				"required": []any{"todoList"},
			},
		},
		{
			Name:        "delegate_task",
			Description: "Delegate a sub-task to a specialized sub-agent. The sub-agent can access the file system and execute tasks independently.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description": map[string]any{"type": "string", "description": "Detailed description of the sub-task. Should contain enough context for the sub-agent to complete it independently."},
				},
				"required": []any{"description"},
			},
		},
		{
			Name:        "askQuestions",
			Description: "Ask the user structured questions to gather required information. Supports single-choice, multi-choice, and free text input.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"questions": map[string]any{
						"type":        "array",
						"description": "List of questions to ask the user.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"header":   map[string]any{"type": "string", "description": "Unique question identifier."},
								"question": map[string]any{"type": "string", "description": "Question text."},
								"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional predefined options."},
							},
							"required": []any{"header", "question"},
						},
					},
				},
				"required": []any{"questions"},
			},
		},
		{
			Name:        "memory",
			Description: "Manage persistent memory across sessions. Supports view, create, str_replace, insert, delete, and rename operations on memory files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"enum":        []any{"view", "create", "str_replace", "insert", "delete", "rename"},
						"description": "Memory operation to perform.",
					},
					"path": map[string]any{"type": "string", "description": "Memory file path (relative to memory root)."},
				},
				"required": []any{"command"},
			},
		},
		{
			Name:        "newWorkspace",
			Description: "Create a new project workspace with scaffolding. Generates the complete project structure based on the provided query.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Project description including technology stack and features."},
				},
				"required": []any{"query"},
			},
		},
		{
			Name:        "resolveMemoryFileUri",
			Description: "Resolve a memory file path to its fully qualified URI. Used for opening memory files in the editor.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Memory file path to resolve."},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "run_skill",
			Description: "Execute a built-in or custom skill by name. Skills are loaded from built-in, workspace, and custom sources.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Name of the skill to execute."},
				},
				"required": []any{"name"},
			},
		},
	}
}
