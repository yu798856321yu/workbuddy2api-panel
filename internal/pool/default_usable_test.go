package pool

import (
	"testing"
	"time"

	"github.com/yu798856321yu/workbuddy2api-panel/internal/auth"
)

// DefaultUIDIfUsable 是「默认号是否要让路给粘性」的判据：可用才返回 uid。
func TestDefaultUIDIfUsable(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "t"})

	// 未设置默认号 → 空串（粘性照常生效）。
	if got := p.DefaultUIDIfUsable("m"); got != "" {
		t.Fatalf("未设置默认号时 = %q, want 空", got)
	}

	p.SetDefaultUID("u2")
	if got := p.DefaultUIDIfUsable("m"); got != "u2" {
		t.Fatalf("健康默认号 = %q, want u2", got)
	}

	// 禁用后不可用 → 空串（让路给粘性/轮换）。
	p.Disable("u2", "test")
	if got := p.DefaultUIDIfUsable("m"); got != "" {
		t.Fatalf("禁用后 = %q, want 空", got)
	}
	p.Revive("u2")

	// 冷却中不可用 → 空串。
	p.Cooldown("u2", CoolSoft, time.Hour, "test")
	if got := p.DefaultUIDIfUsable("m"); got != "" {
		t.Fatalf("冷却中 = %q, want 空", got)
	}
	p.Revive("u2")

	// 该模型被 6004 限额 → 对该模型不可用，对其他模型仍可用。
	p.CooldownSoftForModel("u2", time.Minute, time.Now().Add(time.Hour), "glm-5.3", "6004")
	if got := p.DefaultUIDIfUsable("glm-5.3"); got != "" {
		t.Fatalf("该模型限额中 = %q, want 空", got)
	}
	if got := p.DefaultUIDIfUsable("kimi-k3-1"); got != "u2" {
		t.Fatalf("其他模型 = %q, want u2（模型级豁免应生效）", got)
	}

	// 默认号被移除 → 空串（不 panic、不返回幽灵 uid）。
	p.Remove("u2")
	if got := p.DefaultUIDIfUsable("m"); got != "" {
		t.Fatalf("账号移除后 = %q, want 空", got)
	}
}

