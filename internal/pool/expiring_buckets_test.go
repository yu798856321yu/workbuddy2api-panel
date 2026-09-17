package pool

import (
	"testing"
	"time"

	"github.com/yu798856321yu/workbuddy2api-panel/internal/auth"
)

// 两档分桶的钳制规则：互斥且都属于 credits（7d + 15d ≤ credits）。
func TestSetCreditsDetailedBuckets(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})

	// 正常：两档都在 credits 内。
	p.SetCreditsDetailed("u1", 1000, 1200, 300, 200)
	st, _ := p.Status("u1")
	if st.CreditsExpiring7d != 300 || st.CreditsExpiring15d != 200 {
		t.Fatalf("正常分桶 = %d/%d, want 300/200", st.CreditsExpiring7d, st.CreditsExpiring15d)
	}

	// 负数钳 0。
	p.SetCreditsDetailed("u1", 1000, 1200, -5, -7)
	st, _ = p.Status("u1")
	if st.CreditsExpiring7d != 0 || st.CreditsExpiring15d != 0 {
		t.Fatalf("负数应钳 0, got %d/%d", st.CreditsExpiring7d, st.CreditsExpiring15d)
	}

	// 单档超 credits：钳到 credits。
	p.SetCreditsDetailed("u1", 100, 200, 500, 0)
	st, _ = p.Status("u1")
	if st.CreditsExpiring7d != 100 {
		t.Fatalf("单档超限应钳到 credits, got %d", st.CreditsExpiring7d)
	}

	// 两档之和超 credits：保 7d 档（更紧迫），回缩 15d 档。
	p.SetCreditsDetailed("u1", 100, 200, 80, 90)
	st, _ = p.Status("u1")
	if st.CreditsExpiring7d != 80 {
		t.Errorf("7d 档应保留 80, got %d", st.CreditsExpiring7d)
	}
	if st.CreditsExpiring15d != 20 {
		t.Errorf("15d 档应回缩到 20 (100-80), got %d", st.CreditsExpiring15d)
	}
	if st.CreditsExpiring7d+st.CreditsExpiring15d > st.Credits {
		t.Errorf("两档之和 %d 超过 credits %d", st.CreditsExpiring7d+st.CreditsExpiring15d, st.Credits)
	}

	// 7d 档已占满：15d 档归 0（不能为负）。
	p.SetCreditsDetailed("u1", 100, 200, 100, 50)
	st, _ = p.Status("u1")
	if st.CreditsExpiring7d != 100 || st.CreditsExpiring15d != 0 {
		t.Fatalf("7d 占满时 = %d/%d, want 100/0", st.CreditsExpiring7d, st.CreditsExpiring15d)
	}
}

// 权重：7 天档的加成必须高于 15 天档（同样占比下）。
func TestExpiringWeightTiers(t *testing.T) {
	if expiringWeight7d <= expiringWeight15d {
		t.Fatalf("7d 权重 %.1f 应高于 15d 权重 %.1f", expiringWeight7d, expiringWeight15d)
	}
	if expiringWeight7d >= 10 {
		t.Errorf("7d 权重 %.1f 不应压过 credits 总量项（10）", expiringWeight7d)
	}

	// 同样 50% 占比：7d 档账号的权重必须高于 15d 档账号。
	p := New("")
	p.Add(&auth.Auth{UID: "u7", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "u15", AccessToken: "t"})
	p.SetCreditsDetailed("u7", 1000, 1000, 500, 0)
	p.SetCreditsDetailed("u15", 1000, 1000, 0, 500)

	now := time.Now()
	w7 := p.weightOf(p.byUID["u7"], 1000, now)
	w15 := p.weightOf(p.byUID["u15"], 1000, now)
	if w7 <= w15 {
		t.Fatalf("7d 档权重 %.2f 应高于 15d 档 %.2f", w7, w15)
	}
}

// 两档互斥：同一笔积分不会既计入 7d 又计入 15d（防双重加成）。
func TestExpiringBucketsMutuallyExclusive(t *testing.T) {
	p := New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "t"})
	// 上游给"重叠"数据（300 同时出现在两档）——这是上游分桶异常形态。
	// 池层只保证两档之和不超过 credits，互斥由 upstream 的分桶逻辑保证（见
	// upstream.TestExpiringBucketMutualExclusion）。此处验证池层不会因重叠而崩坏。
	p.SetCreditsDetailed("u1", 300, 300, 300, 300)
	st, _ := p.Status("u1")
	if st.CreditsExpiring7d+st.CreditsExpiring15d > st.Credits {
		t.Fatalf("两档之和 %d 超过 credits %d",
			st.CreditsExpiring7d+st.CreditsExpiring15d, st.Credits)
	}
}
