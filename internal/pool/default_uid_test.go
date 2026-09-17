package pool

import (
	"testing"
	"time"

	"github.com/yu798856321yu/workbuddy2api-panel/internal/auth"
)

// 默认账号：设置后优先选中；不可用时静默回落普通轮换（不报错、不返回 nil）。
func TestDefaultUIDPreferred(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "t"})

	// 未设置默认号：普通轮换（不保证选中谁）。
	if got := p.DefaultUID(); got != "" {
		t.Fatalf("DefaultUID = %q, want 空", got)
	}

	if !p.SetDefaultUID("u2") {
		t.Fatal("SetDefaultUID 应报告有变更")
	}
	if got := p.DefaultUID(); got != "u2" {
		t.Fatalf("DefaultUID = %q, want u2", got)
	}
	// 重复设置同一 uid：无变更。
	if p.SetDefaultUID("u2") {
		t.Error("重复设置同一 uid 应报告无变更")
	}

	// 连续多次 Pick 都应命中默认号（防惊群窗口不适用于显式指定）。
	for i := 0; i < 5; i++ {
		a := p.Pick()
		if a == nil {
			t.Fatal("Pick 返回 nil")
		}
		if a.UID != "u2" {
			t.Fatalf("第 %d 次 Pick = %s, want u2（默认号应被优先选中）", i, a.UID)
		}
	}
}

// 默认号不可用时静默回落：禁用后请求仍能拿到别的账号，而不是失败。
func TestDefaultUIDFallbackWhenDisabled(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "t"})
	p.SetDefaultUID("u2")
	p.Disable("u2", "test")

	a := p.Pick()
	if a == nil {
		t.Fatal("默认号被禁用后应回落到其他账号，而不是返回 nil")
	}
	if a.UID != "u1" {
		t.Fatalf("Pick = %s, want u1（默认号已禁用，应回落）", a.UID)
	}
}

// 默认号冷却中同样回落。
func TestDefaultUIDFallbackWhenCooling(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "t"})
	p.SetDefaultUID("u2")
	p.Cooldown("u2", CoolSoft, time.Hour, "test")

	a := p.Pick()
	if a == nil || a.UID != "u1" {
		t.Fatalf("Pick = %v, want u1（默认号冷却中应回落）", a)
	}
}

// 默认号本轮已试过（tried）时不再重复选它，避免失败后死循环重试同一号。
func TestDefaultUIDRespectsTried(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u2", AccessToken: "t"})
	p.SetDefaultUID("u2")

	a := p.PickExcluding(map[string]bool{"u2": true})
	if a == nil {
		t.Fatal("PickExcluding 返回 nil")
	}
	if a.UID == "u2" {
		t.Fatal("tried 中的默认号不应被再次选中")
	}
}

// 默认号随 state.json 持久化，重启后仍生效。
func TestDefaultUIDPersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	p1 := New(path)
	p1.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p1.SetDefaultUID("u1")
	p1.Flush()
	p1.Close()

	p2 := New(path)
	defer p2.Close()
	if got := p2.DefaultUID(); got != "u1" {
		t.Fatalf("重启后 DefaultUID = %q, want u1", got)
	}
}

// 清除默认号后回到普通轮换。
func TestDefaultUIDClear(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	p.SetDefaultUID("u1")
	if !p.SetDefaultUID("") {
		t.Fatal("清除默认号应报告有变更")
	}
	if got := p.DefaultUID(); got != "" {
		t.Fatalf("清除后 DefaultUID = %q, want 空", got)
	}
}
