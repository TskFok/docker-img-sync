package web

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	syncsvc "github.com/TskFok/DockerImgSync/service/sync"
)

type fakeTaskStore struct {
	tasks        []SyncTask
	logs         []SyncLog
	created      []SyncTask
	updated      []SyncTask
	deleteCount  int
	enabledCalls []bool
}

func (f *fakeTaskStore) ListTasks(ctx context.Context) ([]SyncTask, error) {
	return f.tasks, nil
}

func (f *fakeTaskStore) GetTask(ctx context.Context, id int32) (SyncTask, error) {
	for _, task := range f.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return SyncTask{}, errors.New("任务不存在")
}

func (f *fakeTaskStore) ListLogs(ctx context.Context, taskID int32) ([]SyncLog, error) {
	return f.logs, nil
}

func (f *fakeTaskStore) CreateTask(ctx context.Context, task SyncTask, now time.Time) error {
	f.created = append(f.created, task)
	return nil
}

func (f *fakeTaskStore) UpdateTask(ctx context.Context, task SyncTask, now time.Time) error {
	f.updated = append(f.updated, task)
	return nil
}

func (f *fakeTaskStore) DeleteTask(ctx context.Context, id int32) error {
	f.deleteCount++
	return nil
}

func (f *fakeTaskStore) SetEnabled(ctx context.Context, id int32, enabled bool, now time.Time) error {
	f.enabledCalls = append(f.enabledCalls, enabled)
	return nil
}

type fakeSyncer struct {
	calls []syncCall
	err   error
}

type syncCall struct {
	ctx context.Context
	id  int32
}

func (f *fakeSyncer) SyncNow(ctx context.Context, id int32) error {
	f.calls = append(f.calls, syncCall{ctx: ctx, id: id})
	return f.err
}

func testTaskRouter(tasks *fakeTaskStore, syncer *fakeSyncer) http.Handler {
	if syncer == nil {
		syncer = &fakeSyncer{}
	}
	return NewRouter(Deps{
		Store:         &fakeCredentialStore{creds: []Credential{{ID: 1, Name: "Docker Hub"}}},
		Registries:    &fakeRegistryStore{registries: []Registry{{ID: 1, Name: "杭州"}}},
		Tasks:         tasks,
		Syncer:        syncer,
		AdminUser:     "admin",
		AdminPassword: "admin-pass",
		SessionSecret: "session-secret-must-be-32-characters-min",
	})
}

func taskForm(csrf, image, interval string) url.Values {
	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("name", "nginx")
	form.Set("source_image", image)
	form.Set("registry_id", "1")
	form.Set("interval_seconds", interval)
	return form
}

func postTask(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertNextRunAroundNow(t *testing.T, got *time.Time, start, end time.Time) {
	t.Helper()
	if got == nil {
		t.Fatal("NextRunAt 应被设为当前时间")
	}
	if got.Before(start.Add(-time.Second)) || got.After(end.Add(time.Second)) {
		t.Fatalf("NextRunAt=%s，不在当前时间 %s..%s", got, start, end)
	}
}

func TestTaskCreateRejectsDigestWithoutTag(t *testing.T) {
	store := &fakeTaskStore{}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	image := "nginx@sha256:" + strings.Repeat("a", 64)
	rec := postTask(t, h, cookie, "/tasks", taskForm(csrf, image, "60"))
	if !strings.Contains(rec.Body.String(), "源镜像必须包含 tag") {
		t.Fatalf("正文应含 tag 错误: %s", rec.Body.String())
	}
	if len(store.created) != 0 {
		t.Fatalf("创建次数=%d，应为 0", len(store.created))
	}
}

func TestTaskCreateRejectsShortInterval(t *testing.T) {
	store := &fakeTaskStore{}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	rec := postTask(t, h, cookie, "/tasks", taskForm(csrf, "nginx:latest", "30"))
	if !strings.Contains(rec.Body.String(), "检查间隔至少 60 秒") {
		t.Fatalf("正文应含间隔错误: %s", rec.Body.String())
	}
	if len(store.created) != 0 {
		t.Fatalf("创建次数=%d，应为 0", len(store.created))
	}
}

func TestTaskCreateNormalizesSourceAndSetsNextRun(t *testing.T) {
	store := &fakeTaskStore{}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	start := time.Now()
	rec := postTask(t, h, cookie, "/tasks", taskForm(csrf, "nginx:latest", "60"))
	end := time.Now()
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("创建次数=%d，应为 1", len(store.created))
	}
	got := store.created[0]
	if got.SourceImage != "docker.io/library/nginx:latest" {
		t.Fatalf("SourceImage=%s", got.SourceImage)
	}
	if got.RegistryID != 1 || got.IntervalSeconds != 60 {
		t.Fatalf("任务=%+v", got)
	}
	assertNextRunAroundNow(t, got.NextRunAt, start, end)
}

func TestTaskCreateIntervalZeroClearsNextRun(t *testing.T) {
	store := &fakeTaskStore{}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	rec := postTask(t, h, cookie, "/tasks", taskForm(csrf, "nginx:latest", "0"))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("创建次数=%d，应为 1", len(store.created))
	}
	if store.created[0].NextRunAt != nil {
		t.Fatalf("间隔 0 时 NextRunAt=%v，应为空", store.created[0].NextRunAt)
	}
}

func TestTaskUpdateIntervalZeroToPositiveSetsNextRun(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{{
		ID:              1,
		Name:            "nginx",
		SourceImage:     "docker.io/library/nginx:latest",
		RegistryID:      1,
		IntervalSeconds: 0,
		Enabled:         true,
	}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	start := time.Now()
	rec := postTask(t, h, cookie, "/tasks/1", taskForm(csrf, "nginx:latest", "60"))
	end := time.Now()
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.updated) != 1 {
		t.Fatalf("更新次数=%d，应为 1", len(store.updated))
	}
	assertNextRunAroundNow(t, store.updated[0].NextRunAt, start, end)
}

func TestTaskUpdateClearsLastDigestWhenCopyTargetChanges(t *testing.T) {
	base := SyncTask{
		ID:              1,
		Name:            "nginx",
		SourceImage:     "docker.io/library/nginx:latest",
		RegistryID:      1,
		DestRepository:  "",
		DestTag:         "",
		IntervalSeconds: 60,
		Enabled:         true,
		LastDigest:      "sha256:old",
	}
	cases := []struct {
		name    string
		image   string
		mutate  func(url.Values)
		cleared bool
	}{
		{name: "source_image", image: "redis:latest", cleared: true},
		{name: "registry_id", image: "nginx:latest", mutate: func(form url.Values) { form.Set("registry_id", "2") }, cleared: true},
		{name: "dest_repository", image: "nginx:latest", mutate: func(form url.Values) { form.Set("dest_repository", "mirror/nginx") }, cleared: true},
		{name: "dest_tag", image: "nginx:latest", mutate: func(form url.Values) { form.Set("dest_tag", "stable") }, cleared: true},
		{name: "interval_only", image: "nginx:latest", mutate: func(form url.Values) { form.Set("interval_seconds", "120") }, cleared: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeTaskStore{tasks: []SyncTask{base}}
			h := testTaskRouter(store, nil)
			cookie := doLogin(t, h)
			csrf := loggedInCSRF(t, h, cookie)
			form := taskForm(csrf, tc.image, "60")
			if tc.mutate != nil {
				tc.mutate(form)
			}
			rec := postTask(t, h, cookie, "/tasks/1", form)
			if rec.Code != http.StatusFound {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(store.updated) != 1 {
				t.Fatalf("更新次数=%d，应为 1", len(store.updated))
			}
			got := store.updated[0].LastDigest
			if tc.cleared && got != "" {
				t.Fatalf("LastDigest=%q，变更复制目标后应清空", got)
			}
			if !tc.cleared && got != "sha256:old" {
				t.Fatalf("LastDigest=%q，未改复制目标时应保留", got)
			}
		})
	}
}

func TestTaskUpdateIntervalPositiveToZeroClearsNextRun(t *testing.T) {
	next := time.Now().Add(-time.Minute)
	store := &fakeTaskStore{tasks: []SyncTask{{
		ID:              1,
		Name:            "nginx",
		SourceImage:     "docker.io/library/nginx:latest",
		RegistryID:      1,
		IntervalSeconds: 60,
		Enabled:         true,
		NextRunAt:       &next,
	}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	rec := postTask(t, h, cookie, "/tasks/1", taskForm(csrf, "nginx:latest", "0"))
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.updated) != 1 {
		t.Fatalf("更新次数=%d，应为 1", len(store.updated))
	}
	if store.updated[0].NextRunAt != nil {
		t.Fatalf("间隔改为 0 时 NextRunAt=%v，应为空", store.updated[0].NextRunAt)
	}
}

func TestTaskSyncNowRedirects(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{{ID: 1, Name: "nginx", LastStatus: "idle"}}}
	syncer := &fakeSyncer{}
	h := testTaskRouter(store, syncer)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	form := url.Values{}
	form.Set("csrf", csrf)
	rec := postTask(t, h, cookie, "/tasks/1/sync", form)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/tasks/1" {
		t.Fatalf("Location=%s", loc)
	}
	if len(syncer.calls) != 1 || syncer.calls[0].id != 1 {
		t.Fatalf("SyncNow 调用=%+v", syncer.calls)
	}
	if syncer.calls[0].ctx != context.Background() {
		t.Fatal("SyncNow 应使用 context.Background()，避免请求结束取消复制")
	}
}

func TestTaskSyncNowBusyShowsMessage(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{{ID: 1, Name: "nginx", LastStatus: "running"}}}
	syncer := &fakeSyncer{err: syncsvc.ErrBusy}
	h := testTaskRouter(store, syncer)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	form := url.Values{}
	form.Set("csrf", csrf)
	rec := postTask(t, h, cookie, "/tasks/1/sync", form)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/tasks/1" || u.Query().Get("msg") != "任务正在同步" {
		t.Fatalf("Location=%s", loc)
	}
	req := httptest.NewRequest(http.MethodGet, loc, nil)
	req.AddCookie(cookie)
	detail := httptest.NewRecorder()
	h.ServeHTTP(detail, req)
	if detail.Code != http.StatusOK {
		t.Fatalf("详情 status=%d body=%s", detail.Code, detail.Body.String())
	}
	if !strings.Contains(detail.Body.String(), "任务正在同步") {
		t.Fatalf("详情正文应含正在同步: %s", detail.Body.String())
	}
}

func TestTaskDeleteRunningRejected(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{{ID: 1, Name: "nginx", LastStatus: "running"}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)
	form := url.Values{}
	form.Set("csrf", csrf)
	rec := postTask(t, h, cookie, "/tasks/1/delete", form)
	if !strings.Contains(rec.Body.String(), "任务正在同步，不能删除") {
		t.Fatalf("正文应含删除错误: %s", rec.Body.String())
	}
	if store.deleteCount != 0 {
		t.Fatalf("删除计数=%d，应为 0", store.deleteCount)
	}
}

func TestTaskToggleReenable(t *testing.T) {
	// MySQL SetEnabled：重新启用且 interval_seconds > 0 时把 next_run_at 设为 now。
	// 见 MySQLStore.SetEnabled。本测试不连数据库，只断言假存储收到 enabled=true。
	store := &fakeTaskStore{tasks: []SyncTask{{ID: 1, Name: "nginx", IntervalSeconds: 60, Enabled: true}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	csrf := loggedInCSRF(t, h, cookie)

	off := url.Values{}
	off.Set("csrf", csrf)
	off.Set("enabled", "0")
	if rec := postTask(t, h, cookie, "/tasks/1/toggle", off); rec.Code != http.StatusFound {
		t.Fatalf("关闭 status=%d body=%s", rec.Code, rec.Body.String())
	}

	on := url.Values{}
	on.Set("csrf", csrf)
	on.Set("enabled", "1")
	if rec := postTask(t, h, cookie, "/tasks/1/toggle", on); rec.Code != http.StatusFound {
		t.Fatalf("启用 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.enabledCalls) != 2 || store.enabledCalls[0] || !store.enabledCalls[1] {
		t.Fatalf("SetEnabled 调用=%v，再次启用应为 true", store.enabledCalls)
	}
}

func TestTaskSyncActionFollowsRunningStatus(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{
		{ID: 1, Name: "idle-nginx", LastStatus: "idle"},
		{ID: 2, Name: "running-nginx", LastStatus: "running"},
	}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)

	list := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	list.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("列表 status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	listBody := listRec.Body.String()
	if !strings.Contains(listBody, `action="/tasks/1/sync"`) || !strings.Contains(listBody, "立即同步") {
		t.Fatalf("空闲任务应保留立即同步: %s", listBody)
	}
	if !strings.Contains(listBody, `href="/tasks/2">同步详情</a>`) {
		t.Fatalf("同步中任务应显示同步详情: %s", listBody)
	}
	if strings.Contains(listBody, `action="/tasks/2/sync"`) {
		t.Fatalf("同步中任务不应再提交立即同步: %s", listBody)
	}

	running := httptest.NewRequest(http.MethodGet, "/tasks/2", nil)
	running.AddCookie(cookie)
	runningRec := httptest.NewRecorder()
	h.ServeHTTP(runningRec, running)
	if runningRec.Code != http.StatusOK {
		t.Fatalf("详情 status=%d body=%s", runningRec.Code, runningRec.Body.String())
	}
	runningBody := runningRec.Body.String()
	if !strings.Contains(runningBody, `href="#sync-logs">同步详情</a>`) || !strings.Contains(runningBody, `id="sync-logs"`) {
		t.Fatalf("同步中详情应链到同步记录: %s", runningBody)
	}
	if strings.Contains(runningBody, `action="/tasks/2/sync"`) || strings.Contains(runningBody, "立即同步") {
		t.Fatalf("同步中详情不应再提交立即同步: %s", runningBody)
	}

	idle := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	idle.AddCookie(cookie)
	idleRec := httptest.NewRecorder()
	h.ServeHTTP(idleRec, idle)
	if idleRec.Code != http.StatusOK {
		t.Fatalf("空闲详情 status=%d body=%s", idleRec.Code, idleRec.Body.String())
	}
	idleBody := idleRec.Body.String()
	if !strings.Contains(idleBody, `action="/tasks/1/sync"`) || !strings.Contains(idleBody, "立即同步") {
		t.Fatalf("空闲详情应保留立即同步: %s", idleBody)
	}
}

func TestTaskListShowsStatusAndFailure(t *testing.T) {
	store := &fakeTaskStore{tasks: []SyncTask{{
		ID:         1,
		Name:       "nginx",
		LastStatus: "failed",
		LastError:  "拉取失败",
	}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "failed") || !strings.Contains(body, "拉取失败") {
		t.Fatalf("列表应显示 last_status 和 last_error: %s", body)
	}
}

func TestStatusAndIntervalText(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"idle", "空闲"},
		{"running", "同步中"},
		{"success", "成功"},
		{"failed", "失败"},
		{"skipped", "无变化"},
		{"", "未记录"},
		{"custom", "custom"},
	}
	for _, tc := range cases {
		if got := statusText(tc.status); got != tc.want {
			t.Fatalf("status %q => %q，期望 %q", tc.status, got, tc.want)
		}
	}
	if got := (SyncTask{IntervalSeconds: 0}).IntervalText(); got != "仅手动" {
		t.Fatalf("间隔 0 => %q", got)
	}
	if got := (SyncTask{IntervalSeconds: 60}).IntervalText(); got != "60 秒" {
		t.Fatalf("间隔 60 => %q", got)
	}
	if got := (SyncLog{Trigger: "manual"}).TriggerText(); got != "手动" {
		t.Fatalf("触发 manual => %q", got)
	}
	if got := (SyncLog{Trigger: "schedule"}).TriggerText(); got != "定时" {
		t.Fatalf("触发 schedule => %q", got)
	}
}

func TestTaskListClipsOverflowFields(t *testing.T) {
	name := `发布任务 <b>` + strings.Repeat("名", 80)
	image := "registry.example.com/" + strings.Repeat("library/", 20) + "nginx:1.25"
	repo := strings.Repeat("team/", 30) + "nginx"
	tag := strings.Repeat("release-", 20) + "1"
	lastError := `复制失败 <script>` + strings.Repeat("原因", 40)
	store := &fakeTaskStore{tasks: []SyncTask{{
		ID:             3,
		Name:           name,
		SourceImage:    image,
		DestRepository: repo,
		DestTag:        tag,
		LastStatus:     "failed",
		LastError:      lastError,
	}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="task-list"`,
		`class="clip-visible"`,
		`class="clip-pop" role="tooltip"`,
		`.task-list { table-layout: fixed; }`,
		html.EscapeString(name),
		html.EscapeString(image),
		html.EscapeString(repo),
		html.EscapeString(tag),
		html.EscapeString(lastError),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("列表应包含 %q", want)
		}
	}
	if strings.Contains(body, "<b>") || strings.Contains(body, "<script>") {
		t.Fatal("超长字段中的 HTML 应被转义")
	}
	for _, raw := range []string{name, image, repo, tag, lastError} {
		if strings.Count(body, html.EscapeString(raw)) != 2 {
			t.Fatalf("%q 应同时保留可见文本与悬停全文", raw)
		}
	}
}

func TestTaskDetailClipsOverflowLogText(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	message := `复制失败 <tag attr="x"> ` + strings.Repeat("镜像名", 40)
	store := &fakeTaskStore{
		tasks: []SyncTask{{
			ID:              7,
			Name:            "nginx",
			SourceImage:     "docker.io/library/nginx:latest",
			RegistryID:      1,
			IntervalSeconds: 60,
			Enabled:         true,
			LastStatus:      "failed",
		}},
		logs: []SyncLog{{
			Trigger:      "manual",
			Status:       "failed",
			SourceDigest: digest,
			Message:      message,
			StartedAt:    time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
			FinishedAt:   time.Date(2026, 10, 9, 12, 1, 0, 0, time.UTC),
		}},
	}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/tasks/7", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	escaped := html.EscapeString(message)
	for _, want := range []string{
		`class="logs"`,
		`class="clip-visible"`,
		`class="clip-pop" role="tooltip"`,
		`text-overflow: ellipsis`,
		digest,
		escaped,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("详情页应包含 %q", want)
		}
	}
	if strings.Contains(body, "<tag") {
		t.Fatal("说明中的 HTML 应被转义")
	}
	if strings.Count(body, escaped) != 2 || strings.Count(body, digest) < 2 {
		t.Fatalf("摘要和说明应同时保留可见文本与悬停全文")
	}
}

func TestTaskDetailClipsOverflowFacts(t *testing.T) {
	image := `registry.example.com/<img>` + strings.Repeat("library/", 30) + "nginx:1.25"
	repo := `team/<b>` + strings.Repeat("path/", 40) + "nginx"
	store := &fakeTaskStore{tasks: []SyncTask{{
		ID:             7,
		Name:           "nginx",
		SourceImage:    image,
		DestRepository: repo,
		DestTag:        "stable",
		LastStatus:     "idle",
	}}}
	h := testTaskRouter(store, nil)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/tasks/7", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="facts"`,
		`class="clip-visible"`,
		`class="clip-pop" role="tooltip"`,
		html.EscapeString(image),
		html.EscapeString(repo),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("详情页应包含 %q", want)
		}
	}
	if strings.Contains(body, "<img>") || strings.Contains(body, "<b>") {
		t.Fatal("源镜像和目标仓库名中的 HTML 应被转义")
	}
	for _, raw := range []string{image, repo} {
		if strings.Count(body, html.EscapeString(raw)) != 2 {
			t.Fatalf("%q 应同时保留可见文本与悬停全文", raw)
		}
	}
}

func TestTaskFormShowsSameAsSource(t *testing.T) {
	h := testTaskRouter(&fakeTaskStore{}, nil)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/tasks/new", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Count(rec.Body.String(), "与源相同") < 2 {
		t.Fatalf("目标仓库名和 tag 应说明与源相同: %s", rec.Body.String())
	}
}
