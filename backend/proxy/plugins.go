package proxy

import (
	"agent-router/backend/plugin"
	"agent-router/backend/usage"
)

// pluginLogDeltas converts plugin deltas into the usage-log shape and fills
// the legacy single-delta columns from the first entry (prefer one that
// actually saved tokens, so old readers still see session strip).
func pluginLogDeltas(deltas []plugin.Delta) []usage.PluginDelta {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]usage.PluginDelta, 0, len(deltas))
	for _, d := range deltas {
		if !d.Applied() {
			continue
		}
		out = append(out, usage.PluginDelta{
			PluginID: d.PluginID,
			Before:   d.Before,
			After:    d.After,
			Saved:    d.Saved(),
		})
	}
	return out
}

// applyInputChat runs enabled input plugins over an OpenAI-style request.
// Plugins run in a fixed order: session strip first (drop duplicates), then
// caveman (compress long tool/log/JSON payloads, then inject style rules).
// Each applied plugin contributes one Delta so request_logs can attribute
// savings per plugin. A nil store leaves the request untouched.
func (s *Server) applyInputChat(in Request) (Request, []plugin.Delta) {
	if s.plugins == nil {
		return in, nil
	}
	var deltas []plugin.Delta
	out := in

	if s.plugins.Enabled(plugin.IDSessionStrip) {
		pluginMsgs := make([]plugin.ChatMessage, len(out.Messages))
		for i, m := range out.Messages {
			pluginMsgs[i] = plugin.ChatMessage{
				Role:       m.Role,
				Content:    m.Content,
				ToolCalls:  m.ToolCalls,
				ToolCallID: m.ToolCallID,
			}
		}
		before := estimateChatTokens(pluginMsgs)
		stripped := plugin.StripChatMessages(pluginMsgs)
		after := estimateChatTokens(stripped)
		s.plugins.Record(plugin.IDSessionStrip, before, after)
		deltas = append(deltas, plugin.Delta{PluginID: plugin.IDSessionStrip, Before: before, After: after})
		if len(stripped) != len(out.Messages) {
			msgs := make([]Message, len(stripped))
			for i, m := range stripped {
				msgs[i] = Message{
					Role:       m.Role,
					Content:    m.Content,
					ToolCalls:  m.ToolCalls,
					ToolCallID: m.ToolCallID,
				}
			}
			out.Messages = msgs
		}
	}

	if s.plugins.Enabled(plugin.IDCaveman) {
		level := s.plugins.CavemanLevel()
		pluginMsgs := make([]plugin.ChatMessage, len(out.Messages))
		for i, m := range out.Messages {
			pluginMsgs[i] = plugin.ChatMessage{
				Role:       m.Role,
				Content:    m.Content,
				ToolCalls:  m.ToolCalls,
				ToolCallID: m.ToolCallID,
			}
		}
		before := estimateChatTokens(pluginMsgs)
		// Compress first so the style pack is never cut, then inject.
		pluginMsgs = plugin.CompressChatMessages(pluginMsgs, level)
		pluginMsgs = plugin.InjectCavemanChat(pluginMsgs, plugin.CavemanRules(level))
		after := estimateChatTokens(pluginMsgs)
		s.plugins.Record(plugin.IDCaveman, before, after)
		deltas = append(deltas, plugin.Delta{PluginID: plugin.IDCaveman, Before: before, After: after})
		if len(pluginMsgs) != len(out.Messages) {
			out.Messages = make([]Message, len(pluginMsgs))
		}
		for i, m := range pluginMsgs {
			out.Messages[i] = Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCalls:  m.ToolCalls,
				ToolCallID: m.ToolCallID,
			}
		}
	}

	return out, deltas
}

// applyInputAnthropic is the /v1/messages counterpart.
func (s *Server) applyInputAnthropic(in AnthropicRequest) (AnthropicRequest, []plugin.Delta) {
	if s.plugins == nil {
		return in, nil
	}
	var deltas []plugin.Delta
	out := in

	if s.plugins.Enabled(plugin.IDSessionStrip) {
		pluginMsgs := make([]plugin.AnthropicMessage, len(out.Messages))
		for i, m := range out.Messages {
			pluginMsgs[i] = plugin.AnthropicMessage{Role: m.Role, Content: m.Content}
		}
		before := estimateAnthropicTokens(pluginMsgs, out.System)
		stripped := plugin.StripAnthropicMessages(pluginMsgs)
		after := estimateAnthropicTokens(stripped, out.System)
		s.plugins.Record(plugin.IDSessionStrip, before, after)
		deltas = append(deltas, plugin.Delta{PluginID: plugin.IDSessionStrip, Before: before, After: after})
		if len(stripped) != len(out.Messages) {
			msgs := make([]AnthropicMessage, len(stripped))
			for i, m := range stripped {
				msgs[i] = AnthropicMessage{Role: m.Role, Content: m.Content}
			}
			out.Messages = msgs
		}
	}

	if s.plugins.Enabled(plugin.IDCaveman) {
		level := s.plugins.CavemanLevel()
		pluginMsgs := make([]plugin.AnthropicMessage, len(out.Messages))
		for i, m := range out.Messages {
			pluginMsgs[i] = plugin.AnthropicMessage{Role: m.Role, Content: m.Content}
		}
		before := estimateAnthropicTokens(pluginMsgs, out.System)
		pluginMsgs = plugin.CompressAnthropicMessages(pluginMsgs, level)
		out.System = plugin.InjectCavemanAnthropic(out.System, plugin.CavemanRules(level))
		after := estimateAnthropicTokens(pluginMsgs, out.System)
		s.plugins.Record(plugin.IDCaveman, before, after)
		deltas = append(deltas, plugin.Delta{PluginID: plugin.IDCaveman, Before: before, After: after})
		for i, m := range pluginMsgs {
			out.Messages[i] = AnthropicMessage{Role: m.Role, Content: m.Content}
		}
	}

	return out, deltas
}

func estimateChatTokens(messages []plugin.ChatMessage) int64 {
	var n int64
	for _, m := range messages {
		n += plugin.EstimateTokens(plugin.ContentText(m.Content))
	}
	return n
}

func estimateAnthropicTokens(messages []plugin.AnthropicMessage, system []byte) int64 {
	var n int64
	if len(system) > 0 {
		n += plugin.EstimateTokens(plugin.ContentText(system))
	}
	for _, m := range messages {
		n += plugin.EstimateTokens(plugin.ContentText(m.Content))
	}
	return n
}

// recordPluginOutputStats attributes upstream completion tokens to each
// applied plugin (response arm) and, for known plugins that did not run on
// this request, to the baseline arm so averages can be compared later.
func (s *Server) recordPluginOutputStats(deltas []plugin.Delta, outputTokens int, success bool) {
	if s.plugins == nil || outputTokens <= 0 {
		return
	}
	applied := make(map[string]bool, len(deltas))
	for _, d := range deltas {
		if d.Applied() {
			applied[d.PluginID] = true
			s.plugins.RecordResponse(d.PluginID, int64(outputTokens))
		}
	}
	// Baseline only on successful completions — failures pollute averages.
	if !success {
		return
	}
	for _, id := range []string{plugin.IDSessionStrip, plugin.IDCaveman} {
		if !applied[id] {
			s.plugins.RecordBaseline(id, int64(outputTokens))
		}
	}
}
