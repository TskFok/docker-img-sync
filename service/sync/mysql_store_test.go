package sync

import (
	"errors"
	"testing"
)

func TestSplitDecryptFailuresSkipsAndCollectsIDs(t *testing.T) {
	rows := []joinedTask{
		{ID: 1, SourceImage: "nginx:latest"},
		{ID: 2, SourceImage: "redis:latest"},
		{ID: 3, SourceImage: "alpine:3"},
	}
	tasks, failedIDs := splitDecryptFailures(rows, func(row joinedTask) (Task, error) {
		if row.ID == 2 {
			return Task{}, errors.New("cipher: message authentication failed")
		}
		return Task{ID: row.ID, SourceImage: row.SourceImage}, nil
	})
	if len(tasks) != 2 || tasks[0].ID != 1 || tasks[1].ID != 3 {
		t.Fatalf("应跳过解密失败的任务，得到 %+v", tasks)
	}
	if len(failedIDs) != 1 || failedIDs[0] != 2 {
		t.Fatalf("失败 id=%v，应只含 2，供一次 WHERE id IN 标记", failedIDs)
	}
	if decryptFailedMessage != "登录信息无法解密" {
		t.Fatalf("last_error=%q", decryptFailedMessage)
	}
}

func TestMarkRunningUpdatesStartsAtDigest(t *testing.T) {
	updates := markRunningUpdates()
	if updates["last_status"] != "running" || updates["progress_phase"] != PhaseDigest {
		t.Fatalf("%#v", updates)
	}
}

func TestFinishTaskUpdatesClearsPhaseOnlyWhenFinishedCleanly(t *testing.T) {
	for _, status := range []string{"success", "skipped"} {
		updates := finishTaskUpdates(Result{Status: status, Message: "保留"})
		if updates["progress_phase"] != "" || updates["last_error"] != "" {
			t.Fatalf("%s %#v", status, updates)
		}
	}
	failed := finishTaskUpdates(Result{Status: "failed", Message: "拉取失败"})
	if _, ok := failed["progress_phase"]; ok {
		t.Fatalf("失败不应改阶段: %#v", failed)
	}
	if failed["last_status"] != "failed" || failed["last_error"] != "拉取失败" {
		t.Fatalf("%#v", failed)
	}
}

func TestResetRunningUpdatesKeepsPhase(t *testing.T) {
	updates, status, message, trigger := resetRunningUpdates()
	if status != "failed" || message != InterruptedMessage || trigger != "startup" {
		t.Fatalf("%s %s %s", status, message, trigger)
	}
	if _, ok := updates["progress_phase"]; ok {
		t.Fatalf("重置不应改阶段: %#v", updates)
	}
	if updates["last_status"] != "failed" || updates["last_error"] != InterruptedMessage {
		t.Fatalf("%#v", updates)
	}
}
