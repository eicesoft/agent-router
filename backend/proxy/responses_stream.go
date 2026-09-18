package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// responsesItem 是下发给 Codex 的一个 output item。用 map 而不是结构体，是因为
// Responses 协议对不同类型的 item 用不同字段，且我们只发自己构造的少数几种。
type responsesItem map[string]any

// streamAccumulator 累积一次上游流里的正文、推理内容与工具调用，最后组装成
// Responses 的 output item 数组。
//
// streamAccumulator 累积一次上游流里的正文、推理内容与工具调用，最后组装成
// Responses 的 output item 数组。
//
// 工具调用必须等到流结束才成形：Codex 明确忽略 function_call_arguments.delta 与
// .done，参数的唯一交付路径是 output_item.done 里一份完整的 item（见
// codex-rs/codex-api/src/sse/responses.rs 的 process_responses_event）。
//
// 正文与推理的 id 在第一次出现时就要定下来：Codex 要求 output_text.delta 必须归属
// 一个已经宣告过的 item（否则报「OutputTextDelta without active item」并丢弃
// delta），而宣告用的 added 与收尾的 done 必须是同一个 id。
type streamAccumulator struct {
	text      strings.Builder
	reasoning strings.Builder
	calls     map[int]*openAIToolCall
	order     []int

	textID      string
	reasoningID string
}

func newStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{calls: map[int]*openAIToolCall{}}
}

// textItemID 返回正文 item 的 id，首次调用时分配。
func (a *streamAccumulator) textItemID() string {
	if a.textID == "" {
		a.textID = newItemID("msg")
	}
	return a.textID
}

// reasoningItemID 返回推理 item 的 id，首次调用时分配。
func (a *streamAccumulator) reasoningItemID() string {
	if a.reasoningID == "" {
		a.reasoningID = newItemID("rs")
	}
	return a.reasoningID
}

func (a *streamAccumulator) addToolCalls(raw json.RawMessage) {
	for _, call := range openAIToolCalls(raw) {
		existing, ok := a.calls[call.Index]
		if !ok {
			clone := call
			a.calls[call.Index] = &clone
			a.order = append(a.order, call.Index)
			continue
		}
		// 增量帧里的 name 只在首帧出现，arguments 分多帧拼接。
		if call.ID != "" {
			existing.ID = call.ID
		}
		if call.Name != "" {
			existing.Name = call.Name
		}
		existing.Arguments += call.Arguments
	}
}

// items 按「推理、正文、工具调用」的顺序产出终态 item。真实 Responses API 的顺序与
// 模型行为一致，这里保守地把推理放最前、工具调用放最后：Codex 把它们按序追加进
// 历史，正文先于调用能让 TUI 的展示顺序符合直觉。
//
// 正文与推理复用流式阶段已经宣告出去的 id，让 added 与 done 指向同一个 item。
func (a *streamAccumulator) items(customTools map[string]bool) []responsesItem {
	var items []responsesItem
	if text := a.reasoning.String(); strings.TrimSpace(text) != "" {
		items = append(items, responsesItem{
			"type": "reasoning", "id": a.reasoningItemID(),
			"summary": []any{map[string]any{"type": "summary_text", "text": text}},
		})
	}
	if text := a.text.String(); text != "" {
		items = append(items, responsesItem{
			"type": "message", "id": a.textItemID(), "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text}},
		})
	}
	for _, index := range a.order {
		call := a.calls[index]
		if call.Name == "" {
			continue
		}
		if customTools[call.Name] {
			// freeform 工具：把 arguments 里的 input 展开成 Codex 认的原始字符串。
			items = append(items, responsesItem{
				"type": "custom_tool_call", "id": newItemID("ctc"),
				"call_id": call.ID, "name": call.Name, "input": customToolInput(call.Arguments),
			})
			continue
		}
		items = append(items, responsesItem{
			"type": "function_call", "id": newItemID("fc"),
			"call_id": call.ID, "name": call.Name, "arguments": call.Arguments,
		})
	}
	return items
}

// textItemSkeleton / reasoningItemSkeleton 是流式开头宣告 item 用的空壳。Codex 的
// item 状态机要求 output_text.delta 归属一个已经宣告过的 item，所以第一条 delta
// 之前必须先发一份 added；内容留空，正文靠后续 delta 累积。
func textItemSkeleton(id string) responsesItem {
	return responsesItem{
		"type": "message", "id": id, "role": "assistant",
		"content": []any{},
	}
}

func reasoningItemSkeleton(id string) responsesItem {
	return responsesItem{
		"type": "reasoning", "id": id, "summary": []any{},
	}
}

// customToolInput 从上游返回的 arguments 里取出 freeform 正文。上游可能给
// {"input":"..."}，也可能直接把正文当 arguments 给；两种都认。
func customToolInput(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Input string `json:"input"`
	}
	if json.Unmarshal([]byte(trimmed), &payload) == nil && payload.Input != "" {
		return payload.Input
	}
	return arguments
}

var itemCounter int64

// newItemID 造一个 item id。Responses 的 id 只需在同一会话内唯一且带类型前缀，
// 时间戳加自增计数足够，且不引入随机源（便于测试断言形状）。
func newItemID(prefix string) string {
	itemCounter++
	return fmt.Sprintf("%s_%x", prefix, time.Now().UnixNano()^itemCounter)
}

func newResponseID() string { return newItemID("resp") }

// responsesSSE 写一帧 SSE。Responses 的流不带 event: 行，事件类型在 data 的
// JSON 里（与 Anthropic 的 event: 行写法不同）。
func responsesSSE(w io.Writer, data map[string]any) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", mustJSON(data))
}

// responsesEnvelope 构造事件里那个 response 对象。Codex 从 response.completed 里
// 只读 id 与 usage（见 ResponseCompleted 结构），其余字段是为其他严格客户端与
// curl 调试补齐的形状。
func responsesEnvelope(id, model, status string, items []responsesItem, usage map[string]any) map[string]any {
	if items == nil {
		items = []responsesItem{}
	}
	envelope := map[string]any{
		"id": id, "object": "response", "created_at": time.Now().Unix(),
		"status": status, "model": model, "output": items,
	}
	if usage != nil {
		envelope["usage"] = usage
	}
	return envelope
}

// chatUsageToResponses 把上游的用量转成 Responses 的 usage 形状。
//
// 三个 token 字段都非空是硬要求：Codex 的 ResponseCompletedUsage 里
// input_tokens / output_tokens / total_tokens 都是非 Option 的 i64，缺一个整条
// response.completed 就解析失败；input_tokens_details.cached_tokens 同样是必需的
// 非 Option 字段。因此这里给出的 usage 必须是完整形状。
func chatUsageToResponses(usage tokenUsage) map[string]any {
	return map[string]any{
		"input_tokens":  usage.Input,
		"output_tokens": usage.Output,
		"total_tokens":  usage.Input + usage.Output,
		"input_tokens_details": map[string]any{
			"cached_tokens": usage.CachedInput,
		},
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningOutput,
		},
	}
}

// chatResponseToResponses 转换非流式的上游响应。返回转换后的 JSON 与用量。
func chatResponseToResponses(body []byte, clientModel string, customTools map[string]bool) ([]byte, tokenUsage) {
	var upstream struct {
		Choices []struct {
			Message struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				Reasoning        string          `json:"reasoning"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *usagePayload `json:"usage"`
	}
	usage := tokenUsage{}
	if json.Unmarshal(body, &upstream) != nil {
		// 上游给了 200 但 body 不是 OpenAI 形状：不编造内容，回一个空但合法的
		// response，让 Codex 的解析不至于崩在缺字段上。
		out, _ := json.Marshal(responsesEnvelope(newResponseID(), clientModel, "completed", nil, chatUsageToResponses(usage)))
		return out, usage
	}
	if upstream.Usage != nil {
		usage = upstream.Usage.tokenUsage()
	}

	acc := newStreamAccumulator()
	if len(upstream.Choices) > 0 {
		message := upstream.Choices[0].Message
		acc.text.WriteString(message.Content)
		acc.reasoning.WriteString(message.ReasoningContent)
		if acc.reasoning.Len() == 0 {
			acc.reasoning.WriteString(message.Reasoning)
		}
		acc.addToolCalls(message.ToolCalls)
	}

	out, _ := json.Marshal(responsesEnvelope(newResponseID(), clientModel, "completed", acc.items(customTools), chatUsageToResponses(usage)))
	return out, usage
}

// pipeChatStreamToResponses 逐帧读上游的 Chat Completions SSE，实时转成 Responses
// 事件写给客户端，同时把写出的内容 tee 进 captured 供日志与用量解析。
//
// 返回的 error 只在流确实损坏时非 nil：上游中途断掉、或从未给出 finish_reason。
// 那种情况下不发 response.completed——宁可让 Codex 知道这次交换坏了并重试，也不能
// 让它以为这是正常的回合结束（与 pipeOpenAIStreamToAnthropic 的判断一致）。
func pipeChatStreamToResponses(w io.Writer, body io.ReadCloser, clientModel string, customTools map[string]bool, captured *strings.Builder) (tokenUsage, error) {
	flusher, flushable := w.(http.Flusher)
	dest := w
	if captured != nil {
		dest = io.MultiWriter(w, captured)
	}
	flush := func() {
		if flushable {
			flusher.Flush()
		}
	}

	responseID := newResponseID()
	responsesSSE(dest, map[string]any{
		"type":     "response.created",
		"response": responsesEnvelope(responseID, clientModel, "in_progress", nil, nil),
	})
	flush()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	acc := newStreamAccumulator()
	var usage tokenUsage
	reasoningStarted := false
	textStarted := false
	finishReason := ""
	sawDone := false

	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			sawDone = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string          `json:"content"`
					ReasoningContent string          `json:"reasoning_content"`
					Reasoning        string          `json:"reasoning"`
					ToolCalls        json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *usagePayload `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = usage.merge(chunk.Usage.tokenUsage())
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		reasoning := choice.Delta.ReasoningContent
		if reasoning == "" {
			reasoning = choice.Delta.Reasoning
		}

		// 推理段先于正文/工具出现时开一个 summary part，并宣告推理 item。Codex 的
		// item 状态机要求 delta 归属一个已宣告的 item，否则整条 delta 被丢弃并报
		// 「ReasoningSummaryDelta without active item」。
		if reasoning != "" {
			if !reasoningStarted {
				reasoningStarted = true
				responsesSSE(dest, map[string]any{
					"type": "response.output_item.added",
					"item": reasoningItemSkeleton(acc.reasoningItemID()),
				})
				responsesSSE(dest, map[string]any{
					"type": "response.reasoning_summary_part.added", "summary_index": 0,
				})
			}
			acc.reasoning.WriteString(reasoning)
			responsesSSE(dest, map[string]any{
				"type":  "response.reasoning_summary_text.delta",
				"delta": reasoning, "summary_index": 0,
			})
			flush()
		}
		if choice.Delta.Content != "" {
			if !textStarted {
				textStarted = true
				// 第一条正文 delta 之前必须宣告 item，否则 Codex 报
				// 「OutputTextDelta without active item」并丢掉 delta（流式逐字
				// 显示失效，只剩收尾时那份完整内容兜底）。
				responsesSSE(dest, map[string]any{
					"type": "response.output_item.added",
					"item": textItemSkeleton(acc.textItemID()),
				})
			}
			acc.text.WriteString(choice.Delta.Content)
			responsesSSE(dest, map[string]any{
				"type": "response.output_text.delta", "delta": choice.Delta.Content,
			})
			flush()
		}
		if len(choice.Delta.ToolCalls) > 0 {
			acc.addToolCalls(choice.Delta.ToolCalls)
		}
	}

	if err := scanner.Err(); err != nil {
		writeResponsesFailure(dest, responseID, clientModel, "upstream stream ended early: "+err.Error())
		flush()
		return usage, fmt.Errorf("upstream stream ended early: %w", err)
	}
	// 没有 [DONE] 也没有错误：body 只是结束了。规范的兼容上游不会这样，但只要它
	// 确实给过 finish_reason，就当作一次完整回复收尾。
	if !sawDone && finishReason == "" {
		writeResponsesFailure(dest, responseID, clientModel, "upstream stream ended without a finish_reason")
		flush()
		return usage, fmt.Errorf("upstream stream ended without a finish_reason")
	}

	// 收尾：正文与推理的 item 在流式阶段已经宣告过，这里只补 done；工具调用因为
	// 参数要到流结束才完整，added 与 done 都在这里成对发出。
	items := acc.items(customTools)
	streamed := map[string]bool{}
	if acc.textID != "" {
		streamed[acc.textID] = true
	}
	if acc.reasoningID != "" {
		streamed[acc.reasoningID] = true
	}
	for _, item := range items {
		if id, _ := item["id"].(string); !streamed[id] {
			responsesSSE(dest, map[string]any{"type": "response.output_item.added", "item": item})
		}
		responsesSSE(dest, map[string]any{"type": "response.output_item.done", "item": item})
	}
	responsesSSE(dest, map[string]any{
		"type":     "response.completed",
		"response": responsesEnvelope(responseID, clientModel, "completed", items, chatUsageToResponses(usage)),
	})
	flush()
	return usage, nil
}

// writeResponsesFailure 发一个失败收尾。Codex 解析 response.failed 的
// response.error{type,code,message}，按 code 分类成限流/上下文超限/可重试等，所以
// 这里的 message 用上游给的原话，便于它判断该不该重试。
func writeResponsesFailure(w io.Writer, responseID, clientModel, message string) {
	envelope := responsesEnvelope(responseID, clientModel, "failed", nil, nil)
	envelope["error"] = map[string]any{"message": message, "type": "api_error"}
	responsesSSE(w, map[string]any{"type": "response.failed", "response": envelope})
}
