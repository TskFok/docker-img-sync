package sync

import (
	"context"
	"errors"
	"strings"
	gosync "sync"
	"testing"
	"time"
)

type fakeEngine struct {
	mu        gosync.Mutex
	digest    string
	digestErr error
	copyErr   error
	copyCount int
	dst       string
	srcAuth   *Auth
	dstAuth   *Auth
}

func (f *fakeEngine) Digest(ctx context.Context, ref string, auth *Auth) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.srcAuth = auth
	return f.digest, f.digestErr
}

func (f *fakeEngine) Copy(ctx context.Context, src, dst string, srcAuth, dstAuth *Auth, progress func(phase string) error) error {
	if progress != nil {
		if err := progress(PhaseCopy); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyCount++
	f.dst = dst
	f.srcAuth = srcAuth
	f.dstAuth = dstAuth
	return f.copyErr
}

func fixed(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestRunSkipsSameDigest(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:same"}
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user", Password: "registry-pass"},
		LastDigest:        "sha256:same",
	}, fixed(time.Unix(100, 0)))
	if got.Status != "skipped" || got.Message != "无变化" || got.LastDigest != "sha256:same" || eng.copyCount != 0 {
		t.Fatalf("got %+v copy %d", got, eng.copyCount)
	}
}

func TestRunCopiesWhenDigestChanges(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:new"}
	now := time.Unix(100, 0)
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user", Password: "registry-pass"},
		LastDigest:        "sha256:old",
		IntervalSeconds:   60,
	}, fixed(now))
	if got.Status != "success" || got.LastDigest != "sha256:new" || eng.copyCount != 1 {
		t.Fatalf("got %+v copy %d", got, eng.copyCount)
	}
	if eng.dst != "registry.example.com/ns/nginx:latest" || eng.srcAuth != nil || eng.dstAuth.Username != "dest-user" {
		t.Fatalf("dst %s src %#v dest %#v", eng.dst, eng.srcAuth, eng.dstAuth)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(now.Add(60*time.Second)) || !got.StartedAt.Equal(now) || !got.FinishedAt.Equal(now) {
		t.Fatalf("time %+v", got)
	}
}

func TestRunRedactsPasswordOnDigestError(t *testing.T) {
	eng := &fakeEngine{digestErr: errors.New("login failed for registry-pass")}
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		SourceAuth:        &Auth{Username: "src", Password: "registry-pass"},
		DestAuth:          &Auth{Username: "dest-user", Password: "registry-pass"},
		LastDigest:        "sha256:old",
	}, fixed(time.Unix(100, 0)))
	if got.Status != "failed" || got.LastDigest != "sha256:old" || eng.copyCount != 0 {
		t.Fatalf("got %+v", got)
	}
	if got.Message == "" || strings.Contains(got.Message, "registry-pass") || !strings.Contains(got.Message, "[redacted]") {
		t.Fatalf("message %s", got.Message)
	}
}

func TestRunRequiresDestAuth(t *testing.T) {
	eng := &fakeEngine{}
	got := Run(context.Background(), eng, Task{SourceImage: "nginx:latest"}, fixed(time.Unix(100, 0)))
	if got.Status != "failed" || got.Message != "目标仓库缺少登录信息" || eng.copyCount != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestRunSchedulesNextRunAfterCopyFailure(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:new", copyErr: errors.New("copy failed")}
	now := time.Unix(100, 0)
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user"},
		LastDigest:        "sha256:old",
		IntervalSeconds:   60,
	}, fixed(now))
	if got.Status != "failed" || got.LastDigest != "sha256:old" || got.ObservedDigest != "sha256:new" || eng.copyCount != 1 {
		t.Fatalf("got %+v copy %d", got, eng.copyCount)
	}
	if got.NextRunAt == nil || !got.NextRunAt.Equal(got.FinishedAt.Add(60*time.Second)) {
		t.Fatalf("next run %+v finished %+v", got.NextRunAt, got.FinishedAt)
	}
}

func TestRunManualHasNoNextTime(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:same"}
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user"},
		LastDigest:        "sha256:same",
		IntervalSeconds:   0,
	}, fixed(time.Unix(100, 0)))
	if got.NextRunAt != nil {
		t.Fatal("间隔为 0 时不应有下次运行时间")
	}
}
