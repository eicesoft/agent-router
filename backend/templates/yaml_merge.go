package templates

import (
	"bytes"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// parseOMP decodes omp's models.yml into a YAML node tree, preserving key and
// field order so a merged file keeps the user's original layout. ok is true
// when the document decodes and its root is a mapping.
func parseOMP(data []byte) (*yaml.Node, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	root = *yamlRoot(&root)
	if root.Kind != yaml.MappingNode {
		return nil, false
	}
	return &root, true
}

// mergeOMP stamps the gateway provider into models.yml's providers map. omp
// supports several providers side by side; the gateway (agent-router) is merged
// alongside the user's existing ones, only its own key replaced on re-render.
func mergeOMP(document *yaml.Node, g *Generator) {
	providers := yamlMapping(document, "providers")
	entry := yamlMapping(providers, g.providerName)
	yamlSet(entry, "baseUrl", yamlScalar(g.gateway+"/v1"))
	yamlSet(entry, "apiKey", yamlScalar(envFor(g.providerName)))
	yamlSet(entry, "api", yamlScalar("openai-completions"))
	yamlSet(entry, "auth", yamlScalar("apiKey"))
	yamlSet(entry, "models", ompModels(g.models()))
}

// ompModels renders omp's model list. Window sizes come from the mapping's
// metadata (0 = unset, key omitted so omp falls back to its bundled catalog);
// input is narrowed to the literals omp accepts — text and image, same as pi.
func ompModels(models []Model) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, m := range models {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		yamlSet(item, "id", yamlScalar(m.ID))
		yamlSet(item, "name", yamlScalar(m.Name))
		yamlSet(item, "reasoning", yamlScalar("true"))
		input := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, t := range textImageInput(m.InputTypes) {
			input.Content = append(input.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t})
		}
		yamlSet(item, "input", input)
		if m.InputContextSize > 0 {
			yamlSet(item, "contextWindow", yamlScalar(strconv.Itoa(m.InputContextSize)))
		}
		if m.OutputSize > 0 {
			yamlSet(item, "maxTokens", yamlScalar(strconv.Itoa(m.OutputSize)))
		}
		seq.Content = append(seq.Content, item)
	}
	return seq
}

// yamlRoot unwraps document nodes so callers always work on a plain mapping.
func yamlRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// yamlMapping returns the mapping stored at key inside parent, creating an
// empty one when absent or replacing a non-mapping value, and records it back.
func yamlMapping(parent *yaml.Node, key string) *yaml.Node {
	if node := yamlFind(parent, key); node != nil && node.Kind == yaml.MappingNode {
		return node
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	yamlSet(parent, key, node)
	return node
}

// yamlFind returns the value node for key inside a mapping, or nil.
func yamlFind(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// yamlFindKey returns the key node for key inside a mapping (whose Line
// points at the key's own text), or nil when absent.
func yamlFindKey(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i]
		}
	}
	return nil
}

// yamlSet assigns value to key, replacing an existing entry in place (keeping
// its position) or appending a new one.
func yamlSet(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// yamlScalar builds a tagged scalar so yaml.v3 reprints numbers and booleans
// without quotes; strings default to plain output.
func yamlScalar(v string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	switch {
	case v == "true" || v == "false":
		node.Tag = "!!bool"
	case isYAMLNumber(v):
		node.Tag = "!!int"
	case v == "":
		node.Tag = "!!str"
	default:
		node.Tag = "!!str"
	}
	return node
}

func isYAMLNumber(v string) bool {
	_, err := strconv.ParseInt(v, 10, 64)
	return err == nil
}

// marshalOMP renders a merged models.yml, matching the two-space layout omp's
// own config files use.
func marshalOMP(document *yaml.Node) string {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(document); err != nil {
		return fmt.Sprintf("# agent-router: failed to encode merged config: %v\n", err)
	}
	_ = enc.Close()
	return buf.String()
}
