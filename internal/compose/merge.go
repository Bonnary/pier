// Package compose holds pier's YAML-based docker-compose merge
// primitives. It is intentionally tiny: decode, deep-merge two
// node trees (overlay wins on conflicts), encode, write. The actual
// pier-aware merge that respects "owned services" lives in
// internal/stack/laravel.
package compose

import "gopkg.in/yaml.v3"

// MergeNodes returns the result of deep-merging base and overlay.
// Mappings are merged key-by-key (overlay wins on key collisions),
// sequences are preserved (base wins — user-added lists such as
// volumes or extra_hosts survive), scalars and unknowns are replaced
// with the overlay value. Either argument may be nil; the non-nil one
// is returned as-is.
func MergeNodes(base, overlay *yaml.Node) *yaml.Node {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	return mergeNode(base, overlay)
}

func mergeNode(base, overlay *yaml.Node) *yaml.Node {
	if overlay == nil {
		return base
	}
	if base.Kind != overlay.Kind {
		return overlay
	}
	switch base.Kind {
	case yaml.MappingNode:
		return mergeMapping(base, overlay)
	case yaml.SequenceNode:
		return base
	case yaml.DocumentNode:
		merged := &yaml.Node{Kind: yaml.DocumentNode}
		if len(base.Content) > 0 && len(overlay.Content) > 0 {
			merged.Content = []*yaml.Node{mergeNode(base.Content[0], overlay.Content[0])}
		} else if len(overlay.Content) > 0 {
			merged.Content = overlay.Content
		} else {
			merged.Content = base.Content
		}
		return merged
	default:
		return overlay
	}
}

func mergeMapping(base, overlay *yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	baseIdx := map[string]*yaml.Node{}
	for i := 0; i+1 < len(base.Content); i += 2 {
		baseIdx[base.Content[i].Value] = base.Content[i+1]
	}
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		k := overlay.Content[i]
		v := overlay.Content[i+1]
		out.Content = append(out.Content, k)
		if bv, ok := baseIdx[k.Value]; ok {
			out.Content = append(out.Content, mergeNode(bv, v))
		} else {
			out.Content = append(out.Content, v)
		}
	}
	for i := 0; i+1 < len(base.Content); i += 2 {
		k := base.Content[i]
		if _, ok := baseIdx[k.Value]; !ok {
			continue
		}
		if hasKey(overlay, k.Value) {
			continue
		}
		out.Content = append(out.Content, k, base.Content[i+1])
	}
	return out
}

func hasKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return true
		}
	}
	return false
}
