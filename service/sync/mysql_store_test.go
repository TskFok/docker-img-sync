package sync

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestFinishTaskUpdatesRecordsLastSuccessOnlyOnSuccess(t *testing.T) {
	finished := time.Date(2026, 11, 26, 6, 18, 0, 0, time.UTC)
	success := finishTaskUpdates(Result{Status: "success", FinishedAt: finished})
	got, ok := success["last_success_at"].(time.Time)
	if !ok || !got.Equal(finished) {
		t.Fatalf("成功应写入上次成功时间，得到 %#v", success["last_success_at"])
	}
	for _, status := range []string{"skipped", "failed"} {
		updates := finishTaskUpdates(Result{Status: status, FinishedAt: finished, Message: "未复制"})
		if _, ok := updates["last_success_at"]; ok {
			t.Fatalf("%s 不应改上次成功时间: %#v", status, updates)
		}
		synced, ok := updates["last_synced_at"].(time.Time)
		if !ok || !synced.Equal(finished) {
			t.Fatalf("%s 仍应更新最近同步时间: %#v", status, updates["last_synced_at"])
		}
	}
}

func TestLastSuccessBackfillSQLFillsOnlyMissingTimes(t *testing.T) {
	sql := lastSuccessBackfillSQL("img_sync_task", "img_sync_log")
	for _, want := range []string{
		"UPDATE img_sync_task AS sync_task",
		"FROM img_sync_log",
		"WHERE status = ?",
		"MAX(finished_at)",
		"SET sync_task.last_success_at = latest.finished_at",
		"WHERE sync_task.last_success_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("回填语句缺少 %q\n%s", want, sql)
		}
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
