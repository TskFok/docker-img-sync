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
	mu          gosync.Mutex
	digest      string
	digestErr   error
	digestCalls int
	copyErr     error
	copyCount   int
	dst         string
	srcAuth     *Auth
	dstAuth     *Auth
}

func (f *fakeEngine) Digest(ctx context.Context, ref string, auth *Auth) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.digestCalls++
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

func nopPhase(string) error { return nil }

type phaseReport struct {
	phases []string
	failOn string
}

func (p *phaseReport) save(phase string) error {
	p.phases = append(p.phases, phase)
	if phase == p.failOn {
		return errors.New("dial tcp: password leaked")
	}
	return nil
}

func runnableTask(lastDigest string) Task {
	return Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user", Password: "registry-pass"},
		LastDigest:        lastDigest,
	}
}

func TestRunSkipsSameDigest(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:same"}
	got := Run(context.Background(), eng, Task{
		SourceImage:       "nginx:latest",
		RegistryAddress:   "registry.example.com",
		RegistryNamespace: "ns",
		DestAuth:          &Auth{Username: "dest-user", Password: "registry-pass"},
		LastDigest:        "sha256:same",
	}, fixed(time.Unix(100, 0)), nopPhase)
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
	}, fixed(now), nopPhase)
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
	}, fixed(time.Unix(100, 0)), nopPhase)
	if got.Status != "failed" || got.LastDigest != "sha256:old" || eng.copyCount != 0 {
		t.Fatalf("got %+v", got)
	}
	if got.Message == "" || strings.Contains(got.Message, "registry-pass") || !strings.Contains(got.Message, "[redacted]") {
		t.Fatalf("message %s", got.Message)
	}
}

func TestRunRequiresDestAuth(t *testing.T) {
	eng := &fakeEngine{}
	got := Run(context.Background(), eng, Task{SourceImage: "nginx:latest"}, fixed(time.Unix(100, 0)), nopPhase)
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
	}, fixed(now), nopPhase)
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
	}, fixed(time.Unix(100, 0)), nopPhase)
	if got.NextRunAt != nil {
		t.Fatal("间隔为 0 时不应有下次运行时间")
	}
}

func TestRunSkipRecordsDigestOnly(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:same"}
	report := &phaseReport{}
	got := Run(context.Background(), eng, runnableTask("sha256:same"), fixed(time.Unix(100, 0)), report.save)
	if got.Status != "skipped" || eng.copyCount != 0 {
		t.Fatalf("got %+v copy %d", got, eng.copyCount)
	}
	if strings.Join(report.phases, ",") != PhaseDigest {
		t.Fatalf("phases=%v", report.phases)
	}
}

func TestRunCopyRecordsPhasesBeforeCopy(t *testing.T) {
	eng := &fakeEngine{digest: "sha256:new"}
	report := &phaseReport{}
	got := Run(context.Background(), eng, runnableTask("sha256:old"), fixed(time.Unix(100, 0)), report.save)
	if got.Status != "success" || eng.copyCount != 1 {
		t.Fatalf("got %+v copy %d", got, eng.copyCount)
	}
	if strings.Join(report.phases, ",") != PhaseDigest+","+PhaseManifest+","+PhaseCopy {
		t.Fatalf("phases=%v", report.phases)
	}
}

func TestRunPhaseSaveFailureStops(t *testing.T) {
	cases := []struct {
		name       string
		failOn     string
		lastDigest string
		digest     string
		want       string
		wantDigest int
		wantCopy   int
	}{
		{name: "digest", failOn: PhaseDigest, lastDigest: "sha256:old", digest: "sha256:new", want: PhaseDigest, wantDigest: 0, wantCopy: 0},
		{name: "manifest", failOn: PhaseManifest, lastDigest: "sha256:old", digest: "sha256:new", want: PhaseDigest + "," + PhaseManifest, wantDigest: 1, wantCopy: 0},
		{name: "copy", failOn: PhaseCopy, lastDigest: "sha256:old", digest: "sha256:new", want: PhaseDigest + "," + PhaseManifest + "," + PhaseCopy, wantDigest: 1, wantCopy: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := &fakeEngine{digest: tc.digest}
			report := &phaseReport{failOn: tc.failOn}
			got := Run(context.Background(), eng, runnableTask(tc.lastDigest), fixed(time.Unix(100, 0)), report.save)
			if got.Status != "failed" || got.Message != phaseSaveMessage || strings.Contains(got.Message, "password") {
				t.Fatalf("got %+v", got)
			}
			if strings.Join(report.phases, ",") != tc.want || eng.digestCalls != tc.wantDigest || eng.copyCount != tc.wantCopy {
				t.Fatalf("phases=%v digestCalls=%d copy=%d", report.phases, eng.digestCalls, eng.copyCount)
			}
		})
	}
}

func TestRunEarlyFailureDoesNotReportPhase(t *testing.T) {
	report := &phaseReport{}
	missingAuth := Run(context.Background(), &fakeEngine{}, Task{SourceImage: "nginx:latest"}, fixed(time.Unix(100, 0)), report.save)
	if missingAuth.Status != "failed" || missingAuth.Message != "目标仓库缺少登录信息" || len(report.phases) != 0 {
		t.Fatalf("缺少登录信息 %+v phases=%v", missingAuth, report.phases)
	}
	report = &phaseReport{}
	badRef := Run(context.Background(), &fakeEngine{}, Task{
		SourceImage: "nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DestAuth:    &Auth{Username: "dest-user"},
	}, fixed(time.Unix(100, 0)), report.save)
	if badRef.Status != "failed" || len(report.phases) != 0 {
		t.Fatalf("无法解析 %+v phases=%v", badRef, report.phases)
	}
	report = &phaseReport{}
	badDest := Run(context.Background(), &fakeEngine{}, Task{
		SourceImage: "nginx:latest",
		DestAuth:    &Auth{Username: "dest-user"},
	}, fixed(time.Unix(100, 0)), report.save)
	if badDest.Status != "failed" || len(report.phases) != 0 {
		t.Fatalf("目标引用 %+v phases=%v", badDest, report.phases)
	}
}
