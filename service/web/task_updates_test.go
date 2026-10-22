package web

import (
	"testing"
	"time"
)

func TestTaskEditAndToggleDoNotTouchPhase(t *testing.T) {
	edits := taskFieldUpdates(SyncTask{Name: "nginx"}, time.Unix(1, 0))
	if _, ok := edits["progress_phase"]; ok {
		t.Fatalf("编辑不应写入阶段: %#v", edits)
	}
	if edits["name"] != "nginx" {
		t.Fatalf("编辑应保留任务名: %#v", edits)
	}
	toggle := enabledUpdates(true, time.Unix(1, 0))
	if _, ok := toggle["progress_phase"]; ok {
		t.Fatalf("开关不应写入阶段: %#v", toggle)
	}
	if toggle["enabled"] != true {
		t.Fatalf("开关应写入 enabled: %#v", toggle)
	}
}
