package sync

import "testing"

func TestPhaseText(t *testing.T) {
	cases := []struct {
		phase string
		want  string
	}{
		{PhaseDigest, "检查摘要"},
		{PhaseManifest, "读取清单"},
		{PhaseCopy, "复制镜像"},
		{"", ""},
		{"custom", "custom"},
	}
	for _, tc := range cases {
		if got := PhaseText(tc.phase); got != tc.want {
			t.Fatalf("phase %q => %q，期望 %q", tc.phase, got, tc.want)
		}
	}
	if PhaseDigest != "digest" || PhaseManifest != "manifest" || PhaseCopy != "copy" {
		t.Fatalf("代码 = %s %s %s", PhaseDigest, PhaseManifest, PhaseCopy)
	}
	if errPhaseSave.Error() != "保存同步阶段失败" {
		t.Fatalf("保存阶段失败说明 = %q", errPhaseSave.Error())
	}
}
