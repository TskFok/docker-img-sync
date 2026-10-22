package sync

import (
	"context"
	"errors"
	"fmt"
	gosync "sync"
	"time"
)

const PollInterval = 15 * time.Second

const finishAttempts = 3

const resultUnsavedError = "保存同步结果失败"

var ErrBusy = errors.New("任务正在同步")

type Store interface {
	ListDue(ctx context.Context, now time.Time) ([]Task, error)
	MarkRunning(ctx context.Context, ids []int32) error
	Finish(ctx context.Context, id int32, trigger string, result Result) error
	MarkResultUnsaved(ctx context.Context, id int32, lastError string) error
	ResetRunning(ctx context.Context, now time.Time) error
	Get(ctx context.Context, id int32) (Task, error)
}

type Scheduler struct {
	store    Store
	eng      Engine
	sem      chan struct{}
	mu       gosync.Mutex
	inflight map[int32]struct{}
}

func NewScheduler(store Store, eng Engine) *Scheduler {
	return &Scheduler{
		store:    store,
		eng:      eng,
		sem:      make(chan struct{}, 2),
		inflight: make(map[int32]struct{}),
	}
}

func (s *Scheduler) claimLocked(id int32) bool {
	if _, ok := s.inflight[id]; ok {
		return false
	}
	s.inflight[id] = struct{}{}
	return true
}

func (s *Scheduler) unclaim(id int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, id)
}

func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	tasks, err := s.store.ListDue(ctx, now)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	claimed := make([]Task, 0, len(tasks))
	ids := make([]int32, 0, len(tasks))
	s.mu.Lock()
	for _, task := range tasks {
		if !s.claimLocked(task.ID) {
			continue
		}
		claimed = append(claimed, task)
		ids = append(ids, task.ID)
	}
	s.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	if err := s.store.MarkRunning(ctx, ids); err != nil {
		s.mu.Lock()
		for _, id := range ids {
			delete(s.inflight, id)
		}
		s.mu.Unlock()
		return err
	}
	for _, task := range claimed {
		task := task
		go s.run(ctx, task, "schedule")
	}
	return nil
}

func (s *Scheduler) run(ctx context.Context, task Task, trigger string) {
	defer s.unclaim(task.ID)
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	result := Run(ctx, s.eng, task, time.Now, func(string) error { return nil })
	s.persistResult(task.ID, trigger, result)
}

func (s *Scheduler) persistResult(id int32, trigger string, result Result) {
	for attempt := 0; attempt < finishAttempts; attempt++ {
		if err := s.store.Finish(context.Background(), id, trigger, result); err == nil {
			return
		}
	}
	if err := s.store.MarkResultUnsaved(context.Background(), id, resultUnsavedError); err != nil {
		fmt.Println(resultUnsavedError)
	}
}

func (s *Scheduler) SyncNow(ctx context.Context, id int32) error {
	task, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if task.LastStatus == "running" || !s.claimLocked(id) {
		s.mu.Unlock()
		return ErrBusy
	}
	s.mu.Unlock()
	if err := s.store.MarkRunning(ctx, []int32{id}); err != nil {
		s.unclaim(id)
		return err
	}
	go s.run(ctx, task, "manual")
	return nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.store.ResetRunning(ctx, time.Now()); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.Tick(ctx, now); err != nil {
					fmt.Println(err)
				}
			}
		}
	}()
	return nil
}
