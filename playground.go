package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent-router/backend/apikey"
	"agent-router/backend/proxy"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// playgroundChunkEvent carries one SSE frame from the gateway to the UI. The
// webview cannot read the local gateway itself (wails:// origin, CORS, and the
// gateway key), so the frames are relayed over Wails events instead.
const playgroundChunkEvent = "playground:chunk"

// emitPlayground is a var so tests can capture frames without a Wails runtime
// context (runtime.EventsEmit panics when the context carries no events object).
var emitPlayground = func(ctx context.Context, chunk PlaygroundChunk) {
	runtime.EventsEmit(ctx, playgroundChunkEvent, chunk)
}

// PlaygroundChunk is one SSE data payload the gateway forwarded, or the
// terminator marking the last one (Done). Completion travels as an event
// rather than the binding's return value because the two are not ordered
// against each other: finalizing on the return could drop trailing frames
// still queued on the event channel.
type PlaygroundChunk struct {
	RunID string `json:"runId"`
	Data  string `json:"data"`
	Done  bool   `json:"done,omitempty"`
}

// PlaygroundResult summarises one run; the answer itself has already streamed
// to the UI as chunks.
type PlaygroundResult struct {
	Status    int `json:"status"`
	LatencyMS int `json:"latencyMs"`
}

// firstEnabledKey returns a gateway key the playground can authenticate with.
func (a *App) firstEnabledKey() (apikey.Key, bool) {
	for _, key := range a.keys.List() {
		if key.Enabled && key.Key != "" {
			return key, true
		}
	}
	return apikey.Key{}, false
}

// playgroundURL dials the gateway over the loopback interface. A gateway bound
// to 0.0.0.0 is not reliably reachable at that address on every OS.
func (a *App) playgroundURL() string {
	addr := a.gatewayAddr
	if host, port, ok := strings.Cut(addr, ":"); ok && (host == "0.0.0.0" || host == "::" || host == "") {
		addr = "127.0.0.1:" + port
	}
	return "http://" + addr + "/v1/chat/completions"
}

// PlaygroundChat runs one streaming chat completion through the local gateway
// and relays every SSE frame to the UI. The request goes over HTTP like any
// other client, so it authenticates, logs usage, and exercises the real
// mapping/adapter path.
func (a *App) PlaygroundChat(runID, model string, messages []proxy.Message, reasoningLevel string) (PlaygroundResult, error) {
	started := time.Now()
	key, ok := a.firstEnabledKey()
	if !ok {
		return PlaygroundResult{}, fmt.Errorf("请先在「本地密钥」中生成并启用一个密钥")
	}
	if !a.proxy.Running() {
		return PlaygroundResult{}, fmt.Errorf("本地代理已停止，请先在控制台开启")
	}
	payload, err := json.Marshal(proxy.Request{
		Model:    model,
		Messages: messages,
		Stream:   true,
		// 演练场是普通 OpenAI 客户端，只发 reasoning_effort；空串表示不请求
		// 思考，由上游按默认行为处理。
		ReasoningEffort: reasoningEffort(reasoningLevel),
		StreamOptions:   map[string]bool{"include_usage": true},
	})
	if err != nil {
		return PlaygroundResult{}, err
	}

	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.playgroundMu.Lock()
	a.playgroundCancel = cancel
	a.playgroundMu.Unlock()
	// The UI treats this frame as "the run is over" — it is emitted on every
	// exit path, error or not, so a failed run never leaves the panel stuck
	// showing a live stream.
	defer func() {
		a.playgroundMu.Lock()
		a.playgroundCancel = nil
		a.playgroundMu.Unlock()
		emitPlayground(a.ctx, PlaygroundChunk{RunID: runID, Done: true})
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.playgroundURL(), bytes.NewReader(payload))
	if err != nil {
		return PlaygroundResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key.Key)
	request.Header.Set("User-Agent", "agent-router-playground")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return PlaygroundResult{LatencyMS: int(time.Since(started).Milliseconds())}, nil
		}
		return PlaygroundResult{}, fmt.Errorf("请求本地网关失败：%w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return PlaygroundResult{Status: response.StatusCode}, fmt.Errorf("网关返回 %d：%s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		emitPlayground(a.ctx, PlaygroundChunk{RunID: runID, Data: data})
		if data == "[DONE]" {
			break
		}
	}
	result := PlaygroundResult{Status: response.StatusCode, LatencyMS: int(time.Since(started).Milliseconds())}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return result, fmt.Errorf("读取流失败：%w", err)
	}
	return result, nil
}

// CancelPlayground aborts the run in flight, if any.
func (a *App) CancelPlayground() {
	a.playgroundMu.Lock()
	cancel := a.playgroundCancel
	a.playgroundMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// reasoningEffort 把 UI 的思考档位翻译成 reasoning_effort 值。off 与空串返回
// nil，请求里就不带该字段。
func reasoningEffort(level string) *string {
	switch level {
	case "low", "medium", "high":
		return &level
	default:
		return nil
	}
}
