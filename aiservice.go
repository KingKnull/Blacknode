package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// Inline command translation — latency matters more than depth.
	modelTranslate = anthropic.ModelClaudeHaiku4_5
	// Multi-line analyses (errors, log triage) — use the more capable Sonnet
	// 4.6 with adaptive thinking. Streaming so the UI can render as it lands.
	modelExplain = anthropic.ModelClaudeSonnet4_6

	systemTranslate = `You are a command-line assistant inside Blacknode, an SSH and infrastructure platform. ` +
		`You translate plain-English requests into a single executable shell command for a Linux/macOS host. ` +
		`Reply with ONLY the command — no markdown fences, no preamble, no explanation. ` +
		`If the request is ambiguous or unsafe, reply with a single line starting with '# ' explaining why.`

	systemExplain = `You are an expert SRE and Linux systems engineer assisting a DevOps user inside Blacknode, ` +
		`an SSH and infrastructure command platform. The user pastes terminal output, error messages, log lines, ` +
		`or commands; you explain what's going on, what likely caused it, and concrete next steps. ` +
		`Be concise but complete — use short paragraphs and bullet points. ` +
		`When suggesting commands, format them in fenced code blocks. ` +
		`Never invent file paths, daemon names, or facts you can't infer from the input.`
)

// AIChunk is the streaming payload — the frontend appends `delta` text to the
// currently-rendering message and uses `done` to know when to stop.
type AIChunk struct {
	StreamID string `json:"streamID"`
	Delta    string `json:"delta"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

type AIService struct {
	settings *SettingsService

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewAIService(settings *SettingsService) *AIService {
	return &AIService{settings: settings, cancels: make(map[string]context.CancelFunc)}
}

func (s *AIService) client() (anthropic.Client, error) {
	key, err := s.settings.AnthropicAPIKey()
	if err != nil {
		return anthropic.Client{}, err
	}
	if key == "" {
		return anthropic.Client{}, errors.New("Anthropic API key not configured — set it in Settings")
	}
	return anthropic.NewClient(option.WithAPIKey(key)), nil
}

// IsConfigured is a fast pre-check the UI can use before showing AI affordances.
func (s *AIService) IsConfigured() (bool, error) {
	key, err := s.settings.AnthropicAPIKey()
	if err != nil {
		return false, nil
	}
	return key != "", nil
}

// Translate maps a natural-language description to a single shell command.
// One-shot; small token budget; no streaming since the response is tiny.
func (s *AIService) Translate(prompt, shellHint, hostHint string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is empty")
	}
	c, err := s.client()
	if err != nil {
		return "", err
	}
	ctxLine := strings.TrimSpace(fmt.Sprintf("Shell: %s\nHost context: %s", shellHint, hostHint))
	user := prompt
	if ctxLine != "Shell: \nHost context:" {
		user = ctxLine + "\n\nRequest: " + prompt
	}

	resp, err := c.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     modelTranslate,
		MaxTokens: 512,
		System: []anthropic.TextBlockParam{
			{Text: systemTranslate, CacheControl: anthropic.NewCacheControlEphemeralParam()},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			out.WriteString(t.Text)
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// Explain streams an analysis of pasted output/errors/logs back to the
// frontend via "ai:chunk" events. Returns immediately; the caller listens
// for chunks tagged with the given streamID.
func (s *AIService) Explain(streamID, content, kind string) error {
	if streamID == "" {
		return errors.New("streamID required")
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("content is empty")
	}

	// Cancel any existing stream with the same ID — the UI can re-ask without
	// stacking concurrent calls.
	s.cancel(streamID)

	c, err := s.client()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[streamID] = cancel
	s.mu.Unlock()

	user := content
	if kind != "" {
		user = fmt.Sprintf("Kind: %s\n\n%s", kind, content)
	}

	go func() {
		defer s.cancel(streamID)
		stream := c.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
			Model:     modelExplain,
			MaxTokens: 4096,
			System: []anthropic.TextBlockParam{
				{Text: systemExplain, CacheControl: anthropic.NewCacheControlEphemeralParam()},
			},
			Thinking: anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}},
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
			},
		})

		for stream.Next() {
			ev := stream.Current()
			if delta, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
				if td, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok {
					emitChunk(streamID, td.Text, false, "")
				}
			}
		}
		if err := stream.Err(); err != nil {
			emitChunk(streamID, "", true, err.Error())
			return
		}
		emitChunk(streamID, "", true, "")
	}()

	return nil
}

// Stop terminates an in-flight Explain stream.
func (s *AIService) Stop(streamID string) error {
	s.cancel(streamID)
	return nil
}

func (s *AIService) cancel(streamID string) {
	s.mu.Lock()
	cancel, ok := s.cancels[streamID]
	if ok {
		delete(s.cancels, streamID)
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func emitChunk(streamID, delta string, done bool, errMsg string) {
	if app := application.Get(); app != nil {
		app.Event.Emit("ai:chunk", AIChunk{
			StreamID: streamID, Delta: delta, Done: done, Error: errMsg,
		})
	}
}

