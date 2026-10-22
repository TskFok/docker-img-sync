package sync

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Task struct {
	ID                int32
	SourceImage       string
	DestRepository    string
	DestTag           string
	RegistryAddress   string
	RegistryNamespace string
	SourceAuth        *Auth
	DestAuth          *Auth
	LastDigest        string
	LastStatus        string
	IntervalSeconds   int
}

type Result struct {
	Status         string
	ObservedDigest string
	Message        string
	LastDigest     string
	NextRunAt      *time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
}

func Run(ctx context.Context, eng Engine, task Task, now func() time.Time, report func(phase string) error) (result Result) {
	if now == nil {
		now = time.Now
	}
	result = Result{
		LastDigest: task.LastDigest,
		StartedAt:  now(),
	}

	defer func() {
		result.FinishedAt = now()
		if task.IntervalSeconds > 0 {
			next := result.FinishedAt.Add(time.Duration(task.IntervalSeconds) * time.Second)
			result.NextRunAt = &next
		}
	}()

	save := func(phase string) error {
		if report == nil {
			return nil
		}
		if err := report(phase); err != nil {
			return errPhaseSave
		}
		return nil
	}
	failSave := func() {
		result.Status = "failed"
		result.Message = phaseSaveMessage
	}

	if task.DestAuth == nil {
		result.Status = "failed"
		result.Message = "目标仓库缺少登录信息"
		return result
	}

	normalized, repository, tag, err := ParseSource(task.SourceImage)
	if err != nil {
		result.Status = "failed"
		result.Message = safeMessage(err, task.SourceAuth, task.DestAuth)
		return result
	}

	destRepo := task.DestRepository
	if destRepo == "" {
		destRepo = repository
	}
	destTag := task.DestTag
	if destTag == "" {
		destTag = tag
	}

	dst, err := DestRef(task.RegistryAddress, task.RegistryNamespace, destRepo, destTag)
	if err != nil {
		result.Status = "failed"
		result.Message = safeMessage(err, task.SourceAuth, task.DestAuth)
		return result
	}

	if err := save(PhaseDigest); err != nil {
		failSave()
		return result
	}
	digest, err := eng.Digest(ctx, normalized, task.SourceAuth)
	if err != nil {
		result.Status = "failed"
		result.Message = safeMessage(err, task.SourceAuth, task.DestAuth)
		return result
	}
	result.ObservedDigest = digest

	if digest == task.LastDigest {
		result.Status = "skipped"
		result.Message = "无变化"
		return result
	}

	if err := save(PhaseManifest); err != nil {
		failSave()
		return result
	}
	if err := eng.Copy(ctx, normalized, dst, task.SourceAuth, task.DestAuth, save); err != nil {
		result.Status = "failed"
		if errors.Is(err, errPhaseSave) {
			result.Message = phaseSaveMessage
		} else {
			result.Message = safeMessage(err, task.SourceAuth, task.DestAuth)
		}
		return result
	}

	result.Status = "success"
	result.LastDigest = digest
	result.Message = ""
	return result
}

func safeMessage(err error, auths ...*Auth) string {
	msg := err.Error()
	for _, auth := range auths {
		if auth != nil && auth.Password != "" {
			msg = strings.ReplaceAll(msg, auth.Password, "[redacted]")
		}
	}
	return msg
}
