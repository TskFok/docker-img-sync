package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

//go:embed templates/*.html
var templateFS embed.FS

type CredentialStore interface {
	ListCredentials(ctx context.Context) ([]Credential, error)
	GetCredential(ctx context.Context, id int32) (Credential, error)
	CreateCredential(ctx context.Context, c Credential) error
	UpdateCredential(ctx context.Context, c Credential) error
	DeleteCredential(ctx context.Context, id int32) error
}

type Deps struct {
	Store         CredentialStore
	Registries    RegistryStore
	Tasks         TaskStore
	Syncer        Syncer
	AdminUser     string
	AdminPassword string
	SessionSecret string
}

type server struct {
	deps Deps
	tmpl *template.Template
}

type ctxKey int

const sessionCtxKey ctxKey = 1

type sessionInfo struct {
	User string
	CSRF string
}

func NewRouter(deps Deps) http.Handler {
	s := &server{
		deps: deps,
		tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")),
	}
	r := chi.NewRouter()
	r.Get("/", s.handleRoot)
	r.Get("/login", s.handleLoginGet)
	r.Post("/login", s.handleLoginPost)

	r.Group(func(r chi.Router) {
		r.Use(s.requireLogin)
		r.Use(s.requireCSRF)
		r.Post("/logout", s.handleLogout)
		r.Get("/credentials", s.handleCredentialList)
		r.Get("/credentials/new", s.handleCredentialNew)
		r.Post("/credentials", s.handleCredentialCreate)
		r.Get("/credentials/{id}/edit", s.handleCredentialEdit)
		r.Post("/credentials/{id}/delete", s.handleCredentialDelete)
		r.Post("/credentials/{id}", s.handleCredentialUpdate)
		r.Get("/registries", s.handleRegistryList)
		r.Get("/registries/new", s.handleRegistryNew)
		r.Post("/registries", s.handleRegistryCreate)
		r.Get("/registries/{id}/edit", s.handleRegistryEdit)
		r.Post("/registries/{id}/delete", s.handleRegistryDelete)
		r.Post("/registries/{id}", s.handleRegistryUpdate)
		r.Get("/logs", s.handleLogList)
		r.Get("/tasks", s.handleTaskList)
		r.Get("/tasks/new", s.handleTaskNew)
		r.Post("/tasks", s.handleTaskCreate)
		r.Get("/tasks/{id}", s.handleTaskDetail)
		r.Get("/tasks/{id}/edit", s.handleTaskEdit)
		r.Post("/tasks/{id}/delete", s.handleTaskDelete)
		r.Post("/tasks/{id}/sync", s.handleTaskSync)
		r.Post("/tasks/{id}/toggle", s.handleTaskToggle)
		r.Post("/tasks/{id}", s.handleTaskUpdate)
	})
	return r
}

func (s *server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, csrf, err := s.readSession(r)
		if err != nil || user != s.deps.AdminUser {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), sessionCtxKey, sessionInfo{User: user, CSRF: csrf})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "表单无效", http.StatusBadRequest)
			return
		}
		info := sessionFromCtx(r.Context())
		if !csrfOK(r.PostForm.Get("csrf"), info.CSRF) {
			http.Error(w, "csrf 校验失败", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if user, _, err := s.readSession(r); err == nil && user == s.deps.AdminUser {
		http.Redirect(w, r, "/tasks", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if user, _, err := s.readSession(r); err == nil && user == s.deps.AdminUser {
		http.Redirect(w, r, "/tasks", http.StatusFound)
		return
	}
	csrf, err := s.issueSession(w, "")
	if err != nil {
		http.Error(w, "无法签发会话", http.StatusInternalServerError)
		return
	}
	s.render(w, "login", pageData{CSRF: csrf})
}

func (s *server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单无效", http.StatusBadRequest)
		return
	}
	_, csrf, err := s.readSession(r)
	if err != nil || !csrfOK(r.PostForm.Get("csrf"), csrf) {
		http.Error(w, "csrf 校验失败", http.StatusBadRequest)
		return
	}
	user := r.PostForm.Get("username")
	pass := r.PostForm.Get("password")
	passOK := CheckPassword(pass, s.deps.AdminPassword)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.deps.AdminUser)) == 1
	if !userOK || !passOK {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}
	if _, err := s.issueSession(w, s.deps.AdminUser); err != nil {
		http.Error(w, "无法签发会话", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/tasks", http.StatusFound)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *server) readSession(r *http.Request) (user, csrf string, err error) {
	c, err := r.Cookie("session")
	if err != nil {
		return "", "", err
	}
	return VerifySession(s.deps.SessionSecret, c.Value, time.Now())
}

func (s *server) issueSession(w http.ResponseWriter, user string) (string, error) {
	csrf, err := NewCSRF()
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(24 * time.Hour)
	val, err := SignSession(s.deps.SessionSecret, user, csrf, exp)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
		MaxAge:   24 * 60 * 60,
	})
	return csrf, nil
}

func (s *server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if page, ok := data.(pageData); ok {
		data = withPageMeta(name, page)
	}
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "模板错误", http.StatusInternalServerError)
	}
}

func withPageMeta(name string, page pageData) pageData {
	switch name {
	case "login":
		page.Title = "登录 · 镜像同步"
	case "credentials":
		page.Nav = "credentials"
		page.Title = "登录信息 · 镜像同步"
	case "credential_form":
		page.Nav = "credentials"
		if page.IsEdit {
			page.Title = "编辑登录信息 · 镜像同步"
		} else {
			page.Title = "新建登录信息 · 镜像同步"
		}
	case "registries":
		page.Nav = "registries"
		page.Title = "目标仓库 · 镜像同步"
	case "registry_form":
		page.Nav = "registries"
		if page.IsEdit {
			page.Title = "编辑目标仓库 · 镜像同步"
		} else {
			page.Title = "新建目标仓库 · 镜像同步"
		}
	case "tasks":
		page.Nav = "tasks"
		page.Title = "同步任务 · 镜像同步"
	case "task_form":
		page.Nav = "tasks"
		if page.IsEdit {
			page.Title = "编辑同步任务 · 镜像同步"
		} else {
			page.Title = "新建同步任务 · 镜像同步"
		}
	case "task_detail":
		page.Nav = "tasks"
		page.Title = "同步任务详情 · 镜像同步"
	case "logs":
		page.Nav = "logs"
		page.Title = "同步记录 · 镜像同步"
	}
	return page
}

func sessionFromCtx(ctx context.Context) sessionInfo {
	info, _ := ctx.Value(sessionCtxKey).(sessionInfo)
	return info
}

func csrfOK(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

type pageData struct {
	CSRF        string
	Error       string
	Title       string
	Nav         string
	Action      string
	IsEdit      bool
	Credentials []Credential
	Credential  Credential
	Registries  []Registry
	Registry    Registry
	Tasks       []SyncTask
	Task        SyncTask
	Logs        []SyncLog
	Message     string
	LogPage     int
	LogPages    int
	LogTotal    int
	LogPrevURL  string
	LogNextURL  string
}
