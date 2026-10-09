package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
)

func TestUsageCacheWrite(t *testing.T) {
	got := run(t, "openrouter_cache_write.sse")
	want := llm.Usage{In: 4000, Out: 4, Cached: 1000, CacheWrite: 3000, Cost: f64(0.02)}
	for _, ev := range got {
		if u, ok := ev.(llm.Usage); ok {
			if !reflect.DeepEqual(u, want) {
				t.Fatalf("usage %#v, want %#v", u, want)
			}
			return
		}
	}
	t.Fatal("no usage")
}

// Thinking of every provider is dropped: these servers replay none.
func TestHistorySkipsAllThinking(t *testing.T) {
	msgs := sentMessages(t, []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "go"}}},
		{Role: llm.RoleAssistant, Parts: []llm.Part{
			llm.Thinking{Provider: llm.ProviderAnthropic, Text: "a", Signature: "s"},
			llm.Thinking{Provider: llm.ProviderOpenAI, Signature: "rs_1", Data: "enc"},
			llm.Thinking{Provider: llm.ProviderGemini, Signature: "c2ln"},
			llm.Text{Text: "answer"},
		}},
		{Role: llm.RoleUser, Parts: []llm.Part{llm.Text{Text: "next"}}},
	})
	if len(msgs) != 3 {
		t.Fatalf("messages %v", msgs)
	}
	a := msgs[1]
	if a["content"] != "answer" || len(a) != 2 {
		t.Fatalf("assistant message %v", a)
	}
}

func TestHistoryImages(t *testing.T) {
	jpg := []byte{0xff, 0xd8, 0xff, 0xe0, 'j'}
	p, cap := newProvider(t, serveFile(t, "ollama_text.sse"))
	req := hi()
	req.Images = llmtest.Images{"a1": jpg}
	req.Messages = []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{
		llm.Text{Text: "describe"}, llm.Image{AttachmentID: "a1", MediaType: "image/jpeg"},
	}}}
	s, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, s)
	_, raw, _ := cap.get()
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	c := body.Messages[0].Content
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg)
	if len(c) != 2 || c[0].Type != "text" || c[0].Text != "describe" || c[1].Type != "image_url" || c[1].ImageURL.URL != want {
		t.Fatalf("content %+v", c)
	}
	req.Images = nil
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("image without resolver accepted")
	}
}
