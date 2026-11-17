package tlsfingerprint

import "testing"

// 同内容必须得到同一个键；任一字段变化都必须换键（缓存隔离与模板失效的基础）。
func TestProfileKeyStableAndSensitive(t *testing.T) {
	a := CodexRustlsProfile()
	b := CodexRustlsProfile()
	if ProfileKey(a) != ProfileKey(b) {
		t.Fatalf("相同 profile 摘要不一致: %s vs %s", ProfileKey(a), ProfileKey(b))
	}
	if len(ProfileKey(a)) != 16 {
		t.Fatalf("摘要长度应为 16，得到 %q", ProfileKey(a))
	}

	edited := CodexRustlsProfile()
	edited.Extensions = append([]uint16{16}, edited.Extensions...)
	if ProfileKey(a) == ProfileKey(edited) {
		t.Fatal("扩展顺序变化后摘要必须改变")
	}

	grease := CodexRustlsProfile()
	grease.EnableGREASE = true
	if ProfileKey(a) == ProfileKey(grease) {
		t.Fatal("EnableGREASE 变化后摘要必须改变")
	}

	if ProfileKey(nil) != "" {
		t.Fatal("nil profile 摘要应为空串")
	}
}
