package web

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

var _ CredentialStore = (*MySQLStore)(nil)
var _ RegistryStore = (*MySQLStore)(nil)
var _ TaskStore = (*MySQLStore)(nil)

func (s *MySQLStore) table(name string) string {
	return s.db.NamingStrategy.TableName(name)
}

func (s *MySQLStore) ListCredentials(ctx context.Context) ([]Credential, error) {
	var rows []model.Credential
	if err := s.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Credential, len(rows))
	for i, row := range rows {
		out[i] = Credential{ID: row.ID, Name: row.Name, Username: row.Username}
	}
	return out, nil
}

func (s *MySQLStore) GetCredential(ctx context.Context, id int32) (Credential, error) {
	var row model.Credential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return Credential{}, err
	}
	return Credential{ID: row.ID, Name: row.Name, Username: row.Username}, nil
}

func (s *MySQLStore) CreateCredential(ctx context.Context, c Credential) error {
	enc, err := crypto.Encrypt(s.key, c.Password)
	if err != nil {
		return err
	}
	row := model.Credential{
		Name:              c.Name,
		Username:          c.Username,
		PasswordEncrypted: enc,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *MySQLStore) UpdateCredential(ctx context.Context, c Credential) error {
	updates := map[string]interface{}{
		"name":     c.Name,
		"username": c.Username,
	}
	if c.Password != "" {
		enc, err := crypto.Encrypt(s.key, c.Password)
		if err != nil {
			return err
		}
		updates["password_encrypted"] = enc
	}
	return s.db.WithContext(ctx).Model(&model.Credential{}).Where("id = ?", c.ID).Updates(updates).Error
}

func (s *MySQLStore) DeleteCredential(ctx context.Context, id int32) error {
	var registryCount int64
	if err := s.db.WithContext(ctx).Model(&model.Registry{}).Where("credential_id = ?", id).Count(&registryCount).Error; err != nil {
		return err
	}
	var taskCount int64
	if err := s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("source_credential_id = ?", id).Count(&taskCount).Error; err != nil {
		return err
	}
	if err := model.CanDeleteCredential(int(registryCount), int(taskCount)); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Delete(&model.Credential{}, id).Error
}

type registryRow struct {
	ID             int32
	Name           string
	Address        string
	Namespace      string
	CredentialID   int32
	CredentialName string
}

func (s *MySQLStore) ListRegistries(ctx context.Context) ([]Registry, error) {
	registry := s.table("registry")
	credential := s.table("credential")
	var rows []registryRow
	err := s.db.WithContext(ctx).
		Table(fmt.Sprintf("%s AS registry", registry)).
		Select("registry.id, registry.name, registry.address, registry.namespace, registry.credential_id, credential.name AS credential_name").
		Joins(fmt.Sprintf("LEFT JOIN %s AS credential ON credential.id = registry.credential_id", credential)).
		Order("registry.id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Registry, len(rows))
	for i, row := range rows {
		out[i] = Registry{
			ID:             row.ID,
			Name:           row.Name,
			Address:        row.Address,
			Namespace:      row.Namespace,
			CredentialID:   row.CredentialID,
			CredentialName: row.CredentialName,
		}
	}
	return out, nil
}

func (s *MySQLStore) GetRegistry(ctx context.Context, id int32) (Registry, error) {
	registry := s.table("registry")
	credential := s.table("credential")
	var row registryRow
	err := s.db.WithContext(ctx).
		Table(fmt.Sprintf("%s AS registry", registry)).
		Select("registry.id, registry.name, registry.address, registry.namespace, registry.credential_id, credential.name AS credential_name").
		Joins(fmt.Sprintf("LEFT JOIN %s AS credential ON credential.id = registry.credential_id", credential)).
		Where("registry.id = ?", id).
		Take(&row).Error
	if err != nil {
		return Registry{}, err
	}
	return Registry{
		ID:             row.ID,
		Name:           row.Name,
		Address:        row.Address,
		Namespace:      row.Namespace,
		CredentialID:   row.CredentialID,
		CredentialName: row.CredentialName,
	}, nil
}

func (s *MySQLStore) CreateRegistry(ctx context.Context, r Registry) error {
	row := model.Registry{
		Name:         r.Name,
		Address:      r.Address,
		Namespace:    r.Namespace,
		CredentialID: r.CredentialID,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *MySQLStore) UpdateRegistry(ctx context.Context, r Registry) error {
	return s.db.WithContext(ctx).Model(&model.Registry{}).Where("id = ?", r.ID).Updates(map[string]interface{}{
		"name":          r.Name,
		"address":       r.Address,
		"namespace":     r.Namespace,
		"credential_id": r.CredentialID,
	}).Error
}

func (s *MySQLStore) DeleteRegistry(ctx context.Context, id int32) error {
	var taskCount int64
	if err := s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("registry_id = ?", id).Count(&taskCount).Error; err != nil {
		return err
	}
	if err := model.CanDeleteRegistry(int(taskCount)); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Delete(&model.Registry{}, id).Error
}

func toWebTask(row model.SyncTask) SyncTask {
	return SyncTask{
		ID:                 row.ID,
		Name:               row.Name,
		SourceImage:        row.SourceImage,
		SourceCredentialID: row.SourceCredentialID,
		RegistryID:         row.RegistryID,
		DestRepository:     row.DestRepository,
		DestTag:            row.DestTag,
		IntervalSeconds:    row.IntervalSeconds,
		Enabled:            row.Enabled,
		LastDigest:         row.LastDigest,
		LastStatus:         row.LastStatus,
		LastError:          row.LastError,
		LastSyncedAt:       row.LastSyncedAt,
		NextRunAt:          row.NextRunAt,
	}
}

func (s *MySQLStore) ListTasks(ctx context.Context) ([]SyncTask, error) {
	var rows []model.SyncTask
	if err := s.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SyncTask, len(rows))
	for i, row := range rows {
		out[i] = toWebTask(row)
	}
	return out, nil
}

func (s *MySQLStore) GetTask(ctx context.Context, id int32) (SyncTask, error) {
	var row model.SyncTask
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return SyncTask{}, err
	}
	return toWebTask(row), nil
}

type syncLogRow struct {
	ID           int32
	SyncTaskID   int32
	TaskName     string
	Trigger      string
	Status       string
	SourceDigest string
	Message      string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// ListAllLogs 按 id 倒序返回全部任务的指定页。调用方按该顺序展示，不再排序。
func (s *MySQLStore) ListAllLogs(ctx context.Context, page, size int) ([]SyncLog, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = syncLogPageSize
	}
	logTable := s.table("sync_log")
	taskTable := s.table("sync_task")
	from := fmt.Sprintf("%s AS sync_log", logTable)
	var total int64
	if err := s.db.WithContext(ctx).Table(from).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []syncLogRow
	err := s.db.WithContext(ctx).
		Table(from).
		Select("sync_log.id, sync_log.sync_task_id, sync_task.name AS task_name, sync_log.trigger, sync_log.status, sync_log.source_digest, sync_log.message, sync_log.started_at, sync_log.finished_at").
		Joins(fmt.Sprintf("JOIN %s AS sync_task ON sync_task.id = sync_log.sync_task_id", taskTable)).
		Order("sync_log.id DESC").
		Limit(size).
		Offset((page - 1) * size).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	out := make([]SyncLog, len(rows))
	for i, row := range rows {
		out[i] = SyncLog{
			TaskID:       row.SyncTaskID,
			TaskName:     row.TaskName,
			Trigger:      row.Trigger,
			Status:       row.Status,
			SourceDigest: row.SourceDigest,
			Message:      row.Message,
			StartedAt:    row.StartedAt,
			FinishedAt:   row.FinishedAt,
		}
	}
	return out, int(total), nil
}

func (s *MySQLStore) CreateTask(ctx context.Context, task SyncTask, now time.Time) error {
	if task.LastStatus == "" {
		task.LastStatus = "idle"
	}
	row := model.SyncTask{
		Name:               task.Name,
		SourceImage:        task.SourceImage,
		SourceCredentialID: task.SourceCredentialID,
		RegistryID:         task.RegistryID,
		DestRepository:     task.DestRepository,
		DestTag:            task.DestTag,
		IntervalSeconds:    task.IntervalSeconds,
		Enabled:            task.Enabled,
		LastDigest:         task.LastDigest,
		LastStatus:         task.LastStatus,
		LastError:          task.LastError,
		LastSyncedAt:       task.LastSyncedAt,
		NextRunAt:          task.NextRunAt,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return s.db.WithContext(ctx).
		Select("Name", "SourceImage", "SourceCredentialID", "RegistryID", "DestRepository", "DestTag", "IntervalSeconds", "Enabled", "LastDigest", "LastStatus", "LastError", "LastSyncedAt", "NextRunAt", "CreatedAt", "UpdatedAt").
		Create(&row).Error
}

func (s *MySQLStore) UpdateTask(ctx context.Context, task SyncTask, now time.Time) error {
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
		"name":                 task.Name,
		"source_image":         task.SourceImage,
		"source_credential_id": nullInt32(task.SourceCredentialID),
		"registry_id":          task.RegistryID,
		"dest_repository":      task.DestRepository,
		"dest_tag":             task.DestTag,
		"interval_seconds":     task.IntervalSeconds,
		"enabled":              task.Enabled,
		"last_digest":          task.LastDigest,
		"next_run_at":          nullTime(task.NextRunAt),
		"updated_at":           now,
	}).Error
}

func (s *MySQLStore) DeleteTask(ctx context.Context, id int32) error {
	return s.db.WithContext(ctx).Delete(&model.SyncTask{}, id).Error
}

// SetEnabled 切换任务开关。重新启用且 interval_seconds > 0 时，把 next_run_at 设为 now，
// 使下一次调度会检查（对照 spec：关闭后再启用）。间隔为 0 时不改 next_run_at。
// 关闭只更新 enabled。web 测试用假存储断言 enabled，不连接数据库。
func (s *MySQLStore) SetEnabled(ctx context.Context, id int32, enabled bool, now time.Time) error {
	updates := map[string]interface{}{
		"enabled": enabled,
	}
	if enabled {
		updates["next_run_at"] = gorm.Expr("CASE WHEN interval_seconds > 0 THEN ? ELSE next_run_at END", now)
	}
	return s.db.WithContext(ctx).Model(&model.SyncTask{}).Where("id = ?", id).Updates(updates).Error
}

func nullTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

func nullInt32(v *int32) interface{} {
	if v == nil {
		return nil
	}
	return *v
}
