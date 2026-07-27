package config

// ToolTreeNode represents a node in the tool configuration tree.
// Application-specific tool trees should be defined in the application layer
// using this type and the Helper functions below.
type ToolTreeNode struct {
	Key      string         `json:"key"`                // Node key for enabled-state storage
	Label    string         `json:"label"`              // Display label
	Children []ToolTreeNode `json:"children,omitempty"` // Child nodes
}

// CollectAllKeys recursively collects all keys from a tool tree.
func CollectAllKeys(nodes []ToolTreeNode) []string {
	var keys []string
	for _, n := range nodes {
		keys = append(keys, n.Key)
		if len(n.Children) > 0 {
			keys = append(keys, CollectAllKeys(n.Children)...)
		}
	}
	return keys
}

// FindNodeAncestors finds the path from root to a node by key.
// Returns nil if not found.
func FindNodeAncestors(nodes []ToolTreeNode, key string) []string {
	for _, n := range nodes {
		if n.Key == key {
			return []string{n.Key}
		}
		if len(n.Children) > 0 {
			if path := FindNodeAncestors(n.Children, key); path != nil {
				return append([]string{n.Key}, path...)
			}
		}
	}
	return nil
}

// CollectDescendantKeys collects all descendant keys (including the node itself)
// for a given key in the tree. Returns nil if not found.
func CollectDescendantKeys(nodes []ToolTreeNode, key string) []string {
	for _, n := range nodes {
		if n.Key == key {
			return CollectAllKeys([]ToolTreeNode{n})
		}
		if len(n.Children) > 0 {
			if result := CollectDescendantKeys(n.Children, key); result != nil {
				return result
			}
		}
	}
	return nil
}
