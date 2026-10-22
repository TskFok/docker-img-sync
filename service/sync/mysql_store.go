package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/TskFok/DockerImgSync/app/model"
	"github.com/TskFok/DockerImgSync/utils/crypto"
	"gorm.io/gorm"
)

type MySQLStore struct {
	db  *gorm.DB
	key []byte
}

func NewMySQLStore(db *gorm.DB, key []byte) *MySQLStore {
	return &MySQLStore{db: db, key: key}
}

type joinedTask struct {
	ID                int32
	SourceImage       string
	DestRepository    string
	DestTag           string
	IntervalSeconds   int
	LastDigest        string
	LastStatus        string
	RegistryAddress   string  `gorm:"column:registry_address"`
	RegistryNamespace string  `gorm:"column:registry_namespace"`
	DestUsername      string  `gorm:"column:dest_username"`
	DestPasswordEnc   string  `gorm:"column:dest_password_enc"`
	SourceUsername    *string `gorm:"column:source_username"`
	SourcePasswordEnc *string `gorm:"column:source_password_enc"`
}

func (s *MySQLStore) table(name string) string {
	return s.db.NamingStrategy.TableName(name)
}

func (s *MySQLStore) taskQuery(ctx context.Context) *gorm.DB {
	task := s.table("sync_task")
	registry := s.table("registry")
	credential := s.table("credential")
	return s.db.WithContext(ctx).
		Table(fmt.Sprintf("%s AS sync_task", task)).
		Select([]string{
			"sync_task.id",
			"sync_task.source_image",
			"sync_task.dest_repository",
			"sync_task.dest_tag",
			"sync_task.interval_seconds",
			"sync_task.last_digest",
			"sync_task.last_status",
			"registry.address AS registry_address",
			"registry.namespace AS registry_namespace",
			"dest_cred.username AS dest_username",
			"dest_cred.password_encrypted AS dest_password_enc",
			"src_cred.username AS source_username",
			"src_cred.password_encrypted AS source_password_enc",
		}).
		Joins(fmt.Sprintf("JOIN %s AS registry ON registry.id = sync_task.registry_id", registry)).
		Joins(fmt.Sprintf("JOIN %s AS dest_cred ON dest_cred.id = registry.credential_id", credential)).
		Joins(fmt.Sprintf("LEFT JOIN %s AS src_cred ON src_cred.id = sync_task.source_credential_id", credential))
}

func (s *MySQLStore) toTask(row joinedTask) (Task, error) {
	destPass, err := crypto.Decrypt(s.key, row.DestPasswordEnc)
	if err != nil {
		return Task{}, err
	}
	task := Task{
		ID:                row.ID,
		SourceImage:       row.SourceImage,
		DestRepository:    row.DestRepository,
		DestTag:           row.DestTag,
		RegistryAddress:   row.RegistryAddress,
		RegistryNamespace: row.RegistryNamespace,
		DestAuth:          &Auth{Username: row.DestUsername, Password: destPass},
		LastDigest:        row.LastDigest,
		LastStatus:        row.LastStatus,
		IntervalSeconds:   row.IntervalSeconds,
	}
	if row.SourceUsername != nil && row.SourcePasswordEnc != nil {
		srcPass, err := crypto.Decrypt(s.key, *row.SourcePasswordEnc)
		if err != nil {
			return Task{}, err
		}
		task.SourceAuth = &Auth{Username: *row.SourceUsername, Password: srcPass}
	}
	return task, nil
}

const decryptFailedMessage = "登录信息无法解密"

func splitDecryptFailures(rows []joinedTask, convert func(joinedTask) (Task, error)) (tasks []Task, failedIDs []int32) {
	tasks = make([]Task, 0, len(rows))
	for _, row := range rows {
		task, err := convert(row)
		if err != nil {
			failedIDs = append(failedIDs, row.ID)
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, failedIDs
}

func (s *MySQLStore) ListDue(ctx context.Context, now time.Time) ([]Task, error) {
	var rows []joinedTask
	err := s.taskQuery(ctx).
		Where("sync_task.enabled = ? AND sync_task.interval_seconds > ? AND sync_task.next_run_at <= ? AND sync_task.last_status <> ?", 1, 0, now, "running").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	tasks, failedIDs := splitDecryptFailures(rows, s.toTask)
	if err := s.markDecryptFailed(ctx, failedIDs); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *MySQLStore) markDecryptFailed(ctx context.Context, ids []int32) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		"last_status": "failed",
		"last_error":  decryptFailedMessage,
	}).Error
}

func markRunningUpdates() map[string]interface{} {
	return map[string]interface{}{
		"last_status":    "running",
		"progress_phase": PhaseDigest,
	}
}

func finishTaskUpdates(result Result) map[string]interface{} {
	lastError := result.Message
	if result.Status == "success" || result.Status == "skipped" {
		lastError = ""
	}
	updates := map[string]interface{}{
		"last_status":    result.Status,
		"last_digest":    result.LastDigest,
		"last_error":     lastError,
		"last_synced_at": result.FinishedAt,
		"next_run_at":    result.NextRunAt,
	}
	if result.Status == "success" || result.Status == "skipped" {
		updates["progress_phase"] = ""
	}
	return updates
}

func resetRunningUpdates() (updates map[string]interface{}, status, message, trigger string) {
	status, message, trigger, _ = Interrupted("running")
	updates = map[string]interface{}{
		"last_status": status,
		"last_error":  message,
	}
	return updates, status, message, trigger
}

func (s *MySQLStore) SetPhase(ctx context.Context, id int32, phase string) error {
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id = ?", id).Update("progress_phase", phase).Error
}

func (s *MySQLStore) MarkRunning(ctx context.Context, ids []int32) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id IN ?", ids).Updates(markRunningUpdates()).Error
}

func (s *MySQLStore) MarkResultUnsaved(ctx context.Context, id int32, lastError string) error {
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_status": "failed",
		"last_error":  lastError,
	}).Error
}

func (s *MySQLStore) Finish(ctx context.Context, id int32, trigger string, result Result) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&model.SyncTask{}).Where("id = ?", id).Updates(finishTaskUpdates(result)).Error
		if err != nil {
			return err
		}
		log := model.SyncLog{
			SyncTaskID:   id,
			Trigger:      trigger,
			Status:       result.Status,
			SourceDigest: result.ObservedDigest,
			Message:      result.Message,
			StartedAt:    result.StartedAt,
			FinishedAt:   result.FinishedAt,
		}
		return tx.Create(&log).Error
	})
}

func (s *MySQLStore) ResetRunning(ctx context.Context, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []int32
		if err := tx.Model(&model.SyncTask{}).Where("last_status = ?", "running").Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		updates, status, message, trigger := resetRunningUpdates()
		err := tx.Model(&model.SyncTask{}).Where("id IN ?", ids).Updates(updates).Error
		if err != nil {
			return err
		}
		logs := make([]model.SyncLog, len(ids))
		for i, id := range ids {
			logs[i] = model.SyncLog{
				SyncTaskID: id,
				Trigger:    trigger,
				Status:     status,
				Message:    message,
				StartedAt:  now,
				FinishedAt: now,
			}
		}
		return tx.Create(&logs).Error
	})
}

func (s *MySQLStore) Get(ctx context.Context, id int32) (Task, error) {
	var row joinedTask
	err := s.taskQuery(ctx).Where("sync_task.id = ?", id).Take(&row).Error
	if err != nil {
		return Task{}, err
	}
	return s.toTask(row)
}
