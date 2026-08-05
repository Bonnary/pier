package laravel

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/Bonnary/pier/internal/compose"
	"github.com/Bonnary/pier/internal/config"
)

// Decision is the user's response to a MergeWarning. The decision
// callback the caller passes to MergeDev returns one of these.
type Decision int

const (
	// DecisionKeep means "preserve the user-owned content from the
	// existing docker-compose.yml in the merged output."
	DecisionKeep Decision = iota
	// DecisionDrop means "discard the user-owned content; let
	// pier's fresh render win."
	DecisionDrop
)

// MergeWarning describes one piece of user-owned content found
// during a smart-merge. The laravel stack emits a warning for
// every top-level key in docker-compose.yml that pier does not
// own (services, networks, volumes are owned; everything else is
// not).
type MergeWarning struct {
	Service    string
	Key        string
	SourceFile string
}

func ownedServices(cfg config.Config) map[string]bool {
	out := map[string]bool{"laravel.test": true}
	for _, n := range cfg.Stack.Services {
		out[n] = true
	}
	for n := range cfg.Dev.Services {
		out[n] = true
	}
	for n := range services() {
		out[n] = true
	}
	return out
}

var knownTopLevelKeys = map[string]bool{
	"services": true,
	"networks": true,
	"volumes":  true,
}

// MergeDev renders the fresh dev compose from cfg and merges it
// into existing. When existing is empty, the fresh render is
// returned with no warnings. Otherwise MergeDev walks the existing
// top-level keys: pier-owned keys (services, networks, volumes) are
// merged key-by-key (fresh wins on collisions); every other
// top-level key triggers a MergeWarning that the caller resolves
// via the decision callback. The returned warnings slice includes
// the warnings the caller has not yet seen (regardless of
// decision).
func MergeDev(existing string, cfg config.Config, decision func(MergeWarning) Decision) (string, []MergeWarning, error) {
	files, err := New().GenerateDevCompose(cfg)
	if err != nil {
		return "", nil, err
	}
	var fresh []byte
	for _, f := range files {
		if f.Path == "docker-compose.yml" {
			fresh = f.Contents
			break
		}
	}
	if fresh == nil {
		return "", nil, fmt.Errorf("laravel: fresh dev compose not generated")
	}
	if existing == "" {
		return string(fresh), nil, nil
	}

	var freshNode, existingNode yaml.Node
	if err := yaml.Unmarshal(fresh, &freshNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse fresh: %w", err)
	}
	if err := yaml.Unmarshal([]byte(existing), &existingNode); err != nil {
		return "", nil, fmt.Errorf("laravel: parse existing: %w", err)
	}

	owned := ownedServices(cfg)
	warnings, merged := mergeWithOwnership(&existingNode, &freshNode, owned, decision, "docker-compose.yml")

	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", warnings, err
	}
	return string(out), warnings, nil
}

func mergeWithOwnership(existing, fresh *yaml.Node, owned map[string]bool, decision func(MergeWarning) Decision, sourceFile string) ([]MergeWarning, *yaml.Node) {
	var warnings []MergeWarning
	if existing.Kind == yaml.DocumentNode && len(existing.Content) > 0 {
		existing = existing.Content[0]
	}
	if fresh.Kind == yaml.DocumentNode && len(fresh.Content) > 0 {
		fresh = fresh.Content[0]
	}

	merged := &yaml.Node{Kind: yaml.MappingNode}
	existingMap := map[string]*yaml.Node{}
	for i := 0; i+1 < len(existing.Content); i += 2 {
		existingMap[existing.Content[i].Value] = existing.Content[i+1]
	}
	freshMap := map[string]*yaml.Node{}
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		freshMap[fresh.Content[i].Value] = fresh.Content[i+1]
	}

	for i := 0; i+1 < len(fresh.Content); i += 2 {
		k := fresh.Content[i]
		v := fresh.Content[i+1]
		merged.Content = append(merged.Content, k)
		if k.Value == "services" && v.Kind == yaml.MappingNode {
			mergedServices, svcWarnings := mergeServicesMap(v, existingMap["services"], owned)
			warnings = append(warnings, svcWarnings...)
			merged.Content = append(merged.Content, mergedServices)
			continue
		}
		if existingVal, ok := existingMap[k.Value]; ok {
			merged.Content = append(merged.Content, compose.MergeNodes(existingVal, v))
		} else {
			merged.Content = append(merged.Content, v)
		}
	}

	for k, v := range existingMap {
		if _, ok := freshMap[k]; ok {
			continue
		}
		if knownTopLevelKeys[k] {
			merged.Content = append(merged.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
			continue
		}
		w := MergeWarning{Key: k, SourceFile: sourceFile}
		if decision(w) == DecisionKeep {
			merged.Content = append(merged.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
		}
		warnings = append(warnings, w)
	}

	return warnings, wrapDocument(merged)
}

func mergeServicesMap(fresh, existing *yaml.Node, owned map[string]bool) (*yaml.Node, []MergeWarning) {
	var warnings []MergeWarning
	out := &yaml.Node{Kind: yaml.MappingNode}
	freshMap := map[string]*yaml.Node{}
	for i := 0; i+1 < len(fresh.Content); i += 2 {
		freshMap[fresh.Content[i].Value] = fresh.Content[i+1]
	}
	existingMap := map[string]*yaml.Node{}
	if existing != nil {
		for i := 0; i+1 < len(existing.Content); i += 2 {
			existingMap[existing.Content[i].Value] = existing.Content[i+1]
		}
	}

	for i := 0; i+1 < len(fresh.Content); i += 2 {
		k := fresh.Content[i]
		v := fresh.Content[i+1]
		out.Content = append(out.Content, k)
		if existingVal, ok := existingMap[k.Value]; ok {
			if owned[k.Value] {
				out.Content = append(out.Content, compose.MergeNodes(existingVal, v))
			} else {
				out.Content = append(out.Content, existingVal)
			}
		} else {
			out.Content = append(out.Content, v)
		}
	}

	for k, v := range existingMap {
		if _, ok := freshMap[k]; !ok {
			if owned[k] {
				continue
			}
			out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
		}
	}
	return out, warnings
}

func wrapDocument(n *yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{n}}
}
