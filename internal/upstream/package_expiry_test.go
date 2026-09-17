package upstream

import (
	"testing"
	"time"
)

// packageEndTime 的三字段优先级：CycleEndTime > ExpiredTime > PackageEndTime。
//
// 背景（2026-09-16 实测）：上游对有余额的积分包只填 CycleEndTime，ExpiredTime /
// PackageEndTime 恒为空（只有已用完的包才带）。旧实现只读 PackageEndTime，
// 导致「剩余积分的到期时间」永远解析不出、expiring 分桶恒 0。
func TestPackageEndTimePriority(t *testing.T) {
	cycle := "2026-10-16 11:41:28"
	expired := "2026-09-01 10:26:29"
	pkgEnd := "2026-08-01 00:00:00"

	cases := []struct {
		name                   string
		cycle, expired, pkgEnd string
		want                   string
		wantOK                 bool
	}{
		{"三个都有 → 取 CycleEndTime", cycle, expired, pkgEnd, cycle, true},
		{"无 CycleEndTime → 取 ExpiredTime", "", expired, pkgEnd, expired, true},
		{"仅 PackageEndTime → 取它", "", "", pkgEnd, pkgEnd, true},
		{"全空 → 无到期信息", "", "", "", "", false},
		{"CycleEndTime 格式非法 → 落到 ExpiredTime", "not-a-time", expired, pkgEnd, expired, true},
		{"全部非法 → 无到期信息", "x", "y", "z", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := packageEndTime(c.cycle, c.expired, c.pkgEnd)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if want := mustParse(t, c.want); !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
			// 三个字段都是 UTC+8 墙钟：解析结果必须落在该固定偏移上，
			// 否则跨时区部署会算出偏移 8 小时的到期时刻。
			if _, off := got.Zone(); off != 8*60*60 {
				t.Errorf("zone offset = %d, want 28800 (UTC+8)", off)
			}
		})
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation(packageEndLayout, s, softRateResetLoc)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

// 快过期分桶的判据：窗口内计入、窗口外不计入、无到期信息不计入。
// 这是「优先消耗快到期积分」的数据源，判错会让权重失真。
func TestExpiringBucketBoundary(t *testing.T) {
	now := time.Now().In(softRateResetLoc)
	soon := 168 * time.Hour // 7 天

	within := now.Add(24 * time.Hour).Format(packageEndLayout)      // 窗口内
	beyond := now.Add(30 * 24 * time.Hour).Format(packageEndLayout) // 窗口外
	past := now.Add(-24 * time.Hour).Format(packageEndLayout)       // 已过期

	cases := []struct {
		name      string
		end       string
		wantInExp bool
	}{
		{"窗口内 → 计入 expiring", within, true},
		{"窗口外 → 不计入", beyond, false},
		{"已过期 → 计入（越早到期越该优先消耗）", past, true},
		{"无到期信息 → 不计入", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			end, ok := packageEndTime(c.end, "", "")
			if !ok {
				if c.wantInExp {
					t.Fatal("期望解析成功但失败")
				}
				return
			}
			inWindow := !end.After(now.Add(soon))
			if inWindow != c.wantInExp {
				t.Errorf("in-window = %v, want %v (end=%v)", inWindow, c.wantInExp, end)
			}
		})
	}
}

// 两档分桶的互斥性：≤7 天的积分只进 7d 档，不再重复进 15d 档。
// 若重叠计入，同一笔积分会被加两次权重（7d 档账号被双重放大）。
func TestExpiringBucketMutualExclusion(t *testing.T) {
	now := time.Now().In(softRateResetLoc)
	b := ExpiringBuckets{Within7d: 7 * 24 * time.Hour, Within15d: 15 * 24 * time.Hour}

	cases := []struct {
		name    string
		days    int
		want7d  bool
		want15d bool
	}{
		{"3 天后到期 → 只进 7d 档", 3, true, false},
		{"7 天后到期（边界）→ 只进 7d 档", 7, true, false},
		{"10 天后到期 → 只进 15d 档", 10, false, true},
		{"15 天后到期（边界）→ 只进 15d 档", 15, false, true},
		{"20 天后到期 → 两档都不进", 20, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			end := now.Add(time.Duration(c.days) * 24 * time.Hour)
			// 复刻 UserResourceDetailed 的分桶判定（switch 顺序：先 7d 命中即止）。
			var in7, in15 bool
			switch {
			case b.Within7d > 0 && !end.After(now.Add(b.Within7d)):
				in7 = true
			case b.Within15d > 0 && !end.After(now.Add(b.Within15d)):
				in15 = true
			}
			if in7 != c.want7d {
				t.Errorf("7d 档 = %v, want %v", in7, c.want7d)
			}
			if in15 != c.want15d {
				t.Errorf("15d 档 = %v, want %v", in15, c.want15d)
			}
			if in7 && in15 {
				t.Error("两档同时命中——互斥性被破坏，同一笔积分会被双重加权")
			}
		})
	}
}

// 单档禁用（窗口 0）时该档恒空，另一档照常工作。
func TestExpiringBucketDisableOneTier(t *testing.T) {
	now := time.Now().In(softRateResetLoc)
	end3d := now.Add(3 * 24 * time.Hour)
	end10d := now.Add(10 * 24 * time.Hour)

	// 只开 7d 档：3 天进、10 天不进（15d 档关了）。
	only7 := ExpiringBuckets{Within7d: 7 * 24 * time.Hour}
	var in7, in15 bool
	switch {
	case only7.Within7d > 0 && !end3d.After(now.Add(only7.Within7d)):
		in7 = true
	case only7.Within15d > 0 && !end3d.After(now.Add(only7.Within15d)):
		in15 = true
	}
	if !in7 || in15 {
		t.Errorf("只开 7d 档时 3 天到期应只进 7d 档, got 7d=%v 15d=%v", in7, in15)
	}
	in7, in15 = false, false
	switch {
	case only7.Within7d > 0 && !end10d.After(now.Add(only7.Within7d)):
		in7 = true
	case only7.Within15d > 0 && !end10d.After(now.Add(only7.Within15d)):
		in15 = true
	}
	if in7 || in15 {
		t.Errorf("只开 7d 档时 10 天到期应两档都不进, got 7d=%v 15d=%v", in7, in15)
	}

	// 只开 15d 档：10 天进 15d 档。
	only15 := ExpiringBuckets{Within15d: 15 * 24 * time.Hour}
	in7, in15 = false, false
	switch {
	case only15.Within7d > 0 && !end10d.After(now.Add(only15.Within7d)):
		in7 = true
	case only15.Within15d > 0 && !end10d.After(now.Add(only15.Within15d)):
		in15 = true
	}
	if in7 || !in15 {
		t.Errorf("只开 15d 档时 10 天到期应进 15d 档, got 7d=%v 15d=%v", in7, in15)
	}
}

// 两档都禁用（窗口 0）＝ 完全不分桶，行为与引入分桶前一致。
func TestExpiringBucketAllDisabled(t *testing.T) {
	now := time.Now().In(softRateResetLoc)
	end3d := now.Add(3 * 24 * time.Hour)
	none := ExpiringBuckets{}
	var in7, in15 bool
	switch {
	case none.Within7d > 0 && !end3d.After(now.Add(none.Within7d)):
		in7 = true
	case none.Within15d > 0 && !end3d.After(now.Add(none.Within15d)):
		in15 = true
	}
	if in7 || in15 {
		t.Errorf("两档都禁用时应完全不分桶, got 7d=%v 15d=%v", in7, in15)
	}
}
