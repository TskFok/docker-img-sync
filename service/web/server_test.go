package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/TskFok/DockerImgSync/app/model"
)

type fakeCredentialStore struct {
	creds       []Credential
	deleteCount int
	deleteErr   error
}

func (f *fakeCredentialStore) ListCredentials(ctx context.Context) ([]Credential, error) {
	return f.creds, nil
}

func (f *fakeCredentialStore) GetCredential(ctx context.Context, id int32) (Credential, error) {
	for _, c := range f.creds {
		if c.ID == id {
			return c, nil
		}
	}
	return Credential{}, nil
}

func (f *fakeCredentialStore) CreateCredential(ctx context.Context, c Credential) error {
	return nil
}

func (f *fakeCredentialStore) UpdateCredential(ctx context.Context, c Credential) error {
	return nil
}

func (f *fakeCredentialStore) DeleteCredential(ctx context.Context, id int32) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCount++
	return nil
}

func testRouter(store CredentialStore) http.Handler {
	return NewRouter(Deps{
		Store:         store,
		AdminUser:     "admin",
		AdminPassword: "admin-pass",
		SessionSecret: "session-secret-must-be-32-characters-min",
	})
}

func csrfFromHTML(t *testing.T, body string) string {
	t.Helper()
	re := regexp.MustCompile(`name="csrf"\s+value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if len(m) != 2 {
		re = regexp.MustCompile(`value="([^"]*)"\s+name="csrf"`)
		m = re.FindStringSubmatch(body)
	}
	if len(m) != 2 {
		t.Fatalf("未找到 name=\"csrf\": %s", body)
	}
	return m[1]
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	return nil
}

func getLogin(t *testing.T, h http.Handler) (csrf string, cookie *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login status=%d body=%s", rec.Code, rec.Body.String())
	}
	cookie = sessionCookie(rec)
	if cookie == nil {
		t.Fatal("GET /login 未设置 session cookie")
	}
	return csrfFromHTML(t, rec.Body.String()), cookie
}

func doLogin(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	csrf, cookie := getLogin(t, h)
	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("username", "admin")
	form.Set("password", "admin-pass")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("POST /login status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := sessionCookie(rec)
	if got == nil {
		t.Fatal("登录成功后未 Set-Cookie")
	}
	return got
}

func TestRootRedirectsBySession(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("未登录 status=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("未登录 Location=%s", loc)
	}

	cookie := doLogin(t, h)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("已登录 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/tasks" {
		t.Fatalf("已登录 Location=%s", loc)
	}
}

func TestLoginRedirectUnauthenticated(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location=%s", loc)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	csrf, cookie := getLogin(t, h)
	for _, user := range []string{"admin", "nobody"} {
		form := url.Values{}
		form.Set("csrf", csrf)
		form.Set("username", user)
		form.Set("password", "wrong-pass")
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "用户名或密码错误") {
			t.Fatalf("user=%s status=%d body=%s", user, rec.Code, rec.Body.String())
		}
	}
}

func TestLoginGetRedirectsValidAdmin(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/tasks" {
		t.Fatalf("Location=%s", loc)
	}
	if c := sessionCookie(rec); c != nil {
		t.Fatalf("已登录访问 /login 不应替换会话: %+v", c)
	}
}

func TestLoginPageExplainsWorkflow(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="viewport"`,
		"把镜像复制到目标仓库",
		`name="username"`,
		`name="password"`,
		`for="username"`,
		`for="password"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("登录页应含 %s: %s", want, body)
		}
	}
}

func TestCredentialsNavMarksCurrentStep(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"我的账号",
		"配置顺序",
		`href="/credentials" aria-current="page"`,
		`href="/registries"`,
		`href="/tasks"`,
		`href="/logs"`,
		"同步记录",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("登录信息页应含 %s: %s", want, body)
		}
	}
	if strings.Contains(body, `href="/registries" aria-current="page"`) || strings.Contains(body, `href="/tasks" aria-current="page"`) || strings.Contains(body, `href="/logs" aria-current="page"`) {
		t.Fatalf("当前步骤不应标到其他导航: %s", body)
	}
}

func TestLoginSuccessCredentialsPage(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "我的账号") {
		t.Fatalf("正文应含 我的账号: %s", rec.Body.String())
	}
}

func TestCredentialDeleteWithoutCSRF(t *testing.T) {
	h := testRouter(&fakeCredentialStore{})
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodPost, "/credentials/1/delete", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCredentialDeleteInUse(t *testing.T) {
	store := &fakeCredentialStore{deleteErr: model.ErrInUse}
	h := testRouter(store)
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /credentials status=%d", rec.Code)
	}
	csrf := csrfFromHTML(t, rec.Body.String())
	form := url.Values{}
	form.Set("csrf", csrf)
	del := httptest.NewRequest(http.MethodPost, "/credentials/1/delete", strings.NewReader(form.Encode()))
	del.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	del.AddCookie(cookie)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", delRec.Code, delRec.Body.String())
	}
	if !strings.Contains(delRec.Body.String(), "仍被引用，不能删除") {
		t.Fatalf("正文应含引用错误: %s", delRec.Body.String())
	}
	if store.deleteCount != 0 {
		t.Fatalf("删除计数=%d，应为 0", store.deleteCount)
	}
}

func TestCredentialListClipsOverflowFields(t *testing.T) {
	name := `仓库账号 <b>` + strings.Repeat("名", 80)
	username := `robot$` + strings.Repeat("user-", 40) + `<x>`
	h := testRouter(&fakeCredentialStore{creds: []Credential{{
		ID:       1,
		Name:     name,
		Username: username,
	}}})
	cookie := doLogin(t, h)
	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="credential-list"`,
		`class="clip-visible"`,
		`class="clip-pop" role="tooltip"`,
		`.credential-list { table-layout: fixed; }`,
		html.EscapeString(name),
		html.EscapeString(username),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("列表应包含 %q", want)
		}
	}
	if strings.Contains(body, "<b>") || strings.Contains(body, "<x>") {
		t.Fatal("超长字段中的 HTML 应被转义")
	}
	for _, raw := range []string{name, username} {
		if strings.Count(body, html.EscapeString(raw)) != 2 {
			t.Fatalf("%q 应同时保留可见文本与悬停全文", raw)
		}
	}
}
