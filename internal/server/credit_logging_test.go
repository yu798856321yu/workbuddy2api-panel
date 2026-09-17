package server

import (
	"strings"
	"testing"
	"time"
)

// readCredit 只接受合法的非负数值：缺失与「扣了 0」是两回事（不混为一谈）。
func TestReadCredit(t *testing.T) {
	ok := []struct {
		in   any
		want float64
	}{
		{float64(1.5), 1.5},
		{float64(0), 0},
		{float64(0.0004), 0.0004},
		{int(12), 12},
		{int64(3), 3},
		{float32(2.5), 2.5},
	}
	for _, c := range ok {
		got, has := readCredit(c.in)
		if !has {
			t.Errorf("readCredit(%#v) has=false, want true", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("readCredit(%#v) = %v, want %v", c.in, got, c.want)
		}
	}

	// 这些必须判为"没数据"：nil/负数/bool/字符串/无法解析。
	for _, bad := range []any{nil, float64(-1), float64(-0.5), true, "abc", map[string]any{}, []any{}} {
		if _, has := readCredit(bad); has {
			t.Errorf("readCredit(%#v) has=true, want false", bad)
		}
	}
}

// 流式解析：末帧 usage.credit 被采信；缺失时 Credits() 报告 ok=false。
func TestChatStatsReaderParsesCredit(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"credit":2.25}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	r := newChatStatsReaderSince(strings.NewReader(sse), time.Now())
	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			break
		}
	}
	c, ok := r.Credits()
	if !ok {
		t.Fatal("Credits() ok=false, want true（末帧带 credit）")
	}
	if c != 2.25 {
		t.Errorf("Credits() = %v, want 2.25", c)
	}
	d := r.Usage()
	if !d.HasCredits || d.Credits != 2.25 {
		t.Errorf("Usage().HasCredits/Credits = %v/%v, want true/2.25", d.HasCredits, d.Credits)
	}
}

// 上游未返回 credit（老上游 / 失败请求）时，不得伪装成扣了 0。
func TestChatStatsReaderCreditMissing(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	r := newChatStatsReaderSince(strings.NewReader(sse), time.Now())
	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			break
		}
	}
	if _, ok := r.Credits(); ok {
		t.Error("Credits() ok=true, want false（上游没给 credit 字段）")
	}
	if d := r.Usage(); d.HasCredits {
		t.Error("Usage().HasCredits = true, want false")
	}
}

// 非流式聚合响应里的 usage.credit 同样被提取。
func TestUsageDeltaFromResponseCredit(t *testing.T) {
	resp := map[string]any{
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
			"credit":            float64(0.75),
		},
	}
	d := usageDeltaFromResponse(resp)
	if !d.HasCredits || d.Credits != 0.75 {
		t.Errorf("HasCredits/Credits = %v/%v, want true/0.75", d.HasCredits, d.Credits)
	}
	// 缺失 credit：HasCredits=false，其余 token 字段照常。
	d2 := usageDeltaFromResponse(map[string]any{
		"usage": map[string]any{"prompt_tokens": float64(1)},
	})
	if d2.HasCredits {
		t.Error("无 credit 字段时 HasCredits 应为 false")
	}
	if !d2.HasPromptTokens {
		t.Error("token 字段解析不应受 credit 缺失影响")
	}
}

