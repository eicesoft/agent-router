package templates

import (
	"os"

	"gopkg.in/yaml.v3"
)

// WithBaseline 返回一个 selected 已按磁盘现状设置的 Generator:调用方没有
// 显式勾选(selected == nil)时,从配置文件读出本网关 provider 已写入的模型
// 清单作为基线。这与 SlotBaseline、Codex profile 是同一原则——重开面板必须
// 显示上次写入的结果,否则勾选每次打开都退回全选,像写入丢了选择。
//
// 没有基线(文件缺失、无本网关条目、解析失败)或不是平铺清单形状(pi/ai-sdk/
// omp 以外)时原样返回,调用方保持「默认全选」的旧语义。
//
// 返回值是副本:selected 的修改不落回入参。ListToolTemplates 用同一个
// Generator 遍历所有工具,原地改会把一个工具的基线泄漏给下一个。
func (g *Generator) WithBaseline(t Tool) *Generator {
	if g.selected != nil {
		return g
	}
	var ids []string
	switch t.Shape {
	case "pi", "ai-sdk":
		ids = jsonBaselineIDs(t, g.providerName, t.Shape)
	case "omp":
		ids = ompBaselineIDs(t, g.providerName)
	default:
		return g
	}
	if ids == nil {
		return g
	}
	clone := *g
	return clone.WithModels(ids)
}

// jsonBaselineIDs 读 JSON/JSONC 配置里本网关条目的模型清单(pi 读
// providers.<name>.models 的 id 数组,ai-sdk 读 provider.<name>.models 的
// 对象键)。返回 nil 表示无基线;返回空切片表示用户显式清空过(条目在、
// models 为空),必须保留,不能被当成「没写过」回退成全选。
func jsonBaselineIDs(t Tool, provider, shape string) []string {
	if t.Config == "" {
		return nil
	}
	data, err := os.ReadFile(t.Config)
	if err != nil {
		return nil
	}
	document, ok := parseDoc(data)
	if !ok {
		return nil
	}
	rootKey := "provider" // ai-sdk: provider.<name>.models 是 {id: {...}}
	if shape == "pi" {
		rootKey = "providers" // pi: providers.<name>.models 是 [{id, ...}]
	}
	root, _ := document.vals[rootKey].(*doc)
	if root == nil {
		return nil
	}
	entry, _ := root.vals[provider].(*doc)
	if entry == nil {
		return nil
	}
	raw, exists := entry.vals["models"]
	if !exists {
		return nil
	}
	if shape == "pi" {
		list, ok := raw.([]any)
		if !ok {
			return nil
		}
		ids := make([]string, 0, len(list))
		for _, item := range list {
			if m, ok := item.(*doc); ok {
				if id, ok := m.vals["id"].(string); ok {
					ids = append(ids, id)
				}
			}
		}
		return ids
	}
	models, ok := raw.(*doc)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(models.keys))
	return append(ids, models.keys...)
}

// ompBaselineIDs 读 models.yml 里 providers.<name>.models 的 id 序列,
// 返回约定与 jsonBaselineIDs 一致(nil = 无基线,空 = 显式清空)。
func ompBaselineIDs(t Tool, provider string) []string {
	if t.Config == "" {
		return nil
	}
	data, err := os.ReadFile(t.Config)
	if err != nil {
		return nil
	}
	node, ok := parseOMP(data)
	if !ok {
		return nil
	}
	providers := yamlFind(node, "providers")
	if providers == nil || providers.Kind != yaml.MappingNode {
		return nil
	}
	entry := yamlFind(providers, provider)
	if entry == nil || entry.Kind != yaml.MappingNode {
		return nil
	}
	models := yamlFind(entry, "models")
	if models == nil || models.Kind != yaml.SequenceNode {
		return nil
	}
	ids := make([]string, 0, len(models.Content))
	for _, item := range models.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if id := yamlFind(item, "id"); id != nil && id.Kind == yaml.ScalarNode {
			ids = append(ids, id.Value)
		}
	}
	return ids
}
