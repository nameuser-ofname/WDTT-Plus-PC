package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed ui
var uiFS embed.FS

const (
	cookieName  = "wdtt_admin"
	sessionTTL  = 12 * time.Hour
	serviceName = "wdtt.service"
)

type app struct {
	mu      sync.RWMutex
	cfg     *Config
	cfgPath string
	limiter *loginLimiter
	writeMu sync.Mutex
}

func serve(cfg *Config, cfgPath string) error {
	a := &app{cfg: cfg, cfgPath: cfgPath, limiter: newLoginLimiter()}
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.Handle("GET /api/session", a.auth(a.handleSession))
	mux.Handle("POST /api/admin", a.auth(a.handleAdmin))
	mux.Handle("GET /api/service", a.auth(a.handleServiceStatus))
	mux.Handle("POST /api/service", a.auth(a.handleServiceAction))
	mux.Handle("GET /api/logs", a.auth(a.handleLogs))
	mux.Handle("POST /api/panel-password", a.auth(a.handlePanelPassword))
	mux.Handle("GET /", http.FileServerFS(sub))

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           secure(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("wdtt-admin %s слушает https://%s", version, cfg.Listen)
	certs := &certLoader{cert: cfg.CertFile, key: cfg.KeyFile}
	if _, err := certs.get(nil); err != nil {
		return fmt.Errorf("сертификат: %w", err)
	}
	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.get}
	err = srv.ListenAndServeTLS("", "")
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// secure добавляет защитные заголовки и проверку Origin для небезопасных методов.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost {
			if r.Header.Get("X-Requested-With") != "wdtt-admin" {
				jsonError(w, http.StatusForbidden, "запрос отклонён")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					jsonError(w, http.StatusForbidden, "чужой Origin")
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": msg})
}

func jsonOK(w http.ResponseWriter, v map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	v["ok"] = true
	_ = json.NewEncoder(w).Encode(v)
}

// ---------- сессии ----------

func (a *app) sign(exp int64) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	mac := hmac.New(sha256.New, []byte(a.cfg.SessionSecret))
	// привязка к хешу пароля: смена пароля инвалидирует все сессии
	fmt.Fprintf(mac, "v1|%d|%s", exp, a.cfg.PanelHash)
	return strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *app) validSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	expStr, _, ok := strings.Cut(c.Value, ".")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if !ok || err != nil || time.Now().Unix() > exp {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.sign(exp))) == 1
}

func (a *app) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.validSession(r) {
			jsonError(w, http.StatusUnauthorized, "требуется вход")
			return
		}
		next(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginLimiter struct {
	mu sync.Mutex
	m  map[string]*attempt
}
type attempt struct {
	fails int
	until time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{m: map[string]*attempt{}} }

func (l *loginLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if at := l.m[ip]; at != nil && time.Now().Before(at.until) {
		return time.Until(at.until)
	}
	return 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := l.m[ip]
	if at == nil {
		if len(l.m) > 10000 {
			l.m = map[string]*attempt{}
		}
		at = &attempt{}
		l.m[ip] = at
	}
	at.fails++
	if at.fails >= 5 {
		at.until = time.Now().Add(15 * time.Minute)
		at.fails = 0
	}
}

func (l *loginLimiter) ok(ip string) {
	l.mu.Lock()
	delete(l.m, ip)
	l.mu.Unlock()
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if d := a.limiter.blocked(ip); d > 0 {
		jsonError(w, http.StatusTooManyRequests, fmt.Sprintf("слишком много попыток, подождите %d мин.", int(d.Minutes())+1))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "некорректный запрос")
		return
	}
	a.mu.RLock()
	ok := a.cfg.checkPanelPassword(req.Password)
	a.mu.RUnlock()
	if !ok {
		a.limiter.fail(ip)
		log.Printf("неудачный вход с %s", ip)
		time.Sleep(500 * time.Millisecond)
		jsonError(w, http.StatusUnauthorized, "неверный пароль")
		return
	}
	a.limiter.ok(ip)
	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: a.sign(exp), Path: "/", Expires: time.Unix(exp, 0),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	jsonOK(w, map[string]any{})
}

func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	jsonOK(w, map[string]any{})
}

func (a *app) handleSession(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"version": version})
}

func (a *app) handlePanelPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "некорректный запрос")
		return
	}
	if len(req.New) < 8 {
		jsonError(w, http.StatusBadRequest, "новый пароль должен быть не короче 8 символов")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.checkPanelPassword(req.Current) {
		jsonError(w, http.StatusForbidden, "текущий пароль неверен")
		return
	}
	next := *a.cfg
	if err := next.setPanelPassword(req.New); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := next.save(a.cfgPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "не удалось сохранить: "+err.Error())
		return
	}
	*a.cfg = next
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	jsonOK(w, map[string]any{"message": "Пароль изменён, войдите заново"})
}

// ---------- управление wdtt-server ----------

func (a *app) handleAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cmd   string            `json:"cmd"`
		Flags map[string]string `json:"flags"`
	}
	if err := decode(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "некорректный запрос")
		return
	}
	if !allowedCommands[req.Cmd] {
		jsonError(w, http.StatusBadRequest, "команда не разрешена: "+req.Cmd)
		return
	}
	for k := range req.Flags {
		if k == "" || strings.ContainsAny(k, "= \t\n") || strings.HasPrefix(k, "-") {
			jsonError(w, http.StatusBadRequest, "недопустимое имя параметра")
			return
		}
	}
	a.mu.RLock()
	cfg := *a.cfg
	a.mu.RUnlock()
	// Изменения последовательны: так проще рассуждать о состоянии базы.
	if req.Cmd != "list" && req.Cmd != "details" && req.Cmd != "backup-status" && req.Cmd != "backup-list" {
		a.writeMu.Lock()
		defer a.writeMu.Unlock()
		log.Printf("admin: %s (с %s)", req.Cmd, clientIP(r))
	}
	raw, err := callAdmin(&cfg, buildArgs(req.Cmd, req.Flags))
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(raw)
}

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (a *app) handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	state, _ := run(5*time.Second, "systemctl", "is-active", serviceName)
	enabled, _ := run(5*time.Second, "systemctl", "is-enabled", serviceName)
	since, _ := run(5*time.Second, "systemctl", "show", serviceName, "-p", "ActiveEnterTimestamp", "--value")
	jsonOK(w, map[string]any{"active": state, "enabled": enabled, "since": since})
}

func (a *app) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := decode(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "некорректный запрос")
		return
	}
	switch req.Action {
	case "restart", "start", "stop":
	default:
		jsonError(w, http.StatusBadRequest, "неизвестное действие")
		return
	}
	log.Printf("service %s (с %s)", req.Action, clientIP(r))
	out, err := run(60*time.Second, "systemctl", req.Action, serviceName)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "systemctl: "+out)
		return
	}
	jsonOK(w, map[string]any{"message": "Выполнено: " + req.Action})
}

func (a *app) handleLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n < 20 || n > 1000 {
		n = 200
	}
	out, err := run(15*time.Second, "journalctl", "-u", serviceName, "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso")
	if err != nil && out == "" {
		jsonError(w, http.StatusInternalServerError, "journalctl: "+err.Error())
		return
	}
	jsonOK(w, map[string]any{"logs": out})
}

// certLoader перечитывает сертификат при изменении файла, чтобы продление
// Let's Encrypt подхватывалось без перезапуска панели.
type certLoader struct {
	mu        sync.Mutex
	cert, key string
	loaded    *tls.Certificate
	mtime     time.Time
}

func (c *certLoader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := os.Stat(c.cert)
	if err == nil && c.loaded != nil && st.ModTime().Equal(c.mtime) {
		return c.loaded, nil
	}
	pair, lerr := tls.LoadX509KeyPair(c.cert, c.key)
	if lerr != nil {
		if c.loaded != nil {
			return c.loaded, nil
		}
		return nil, lerr
	}
	c.loaded, c.mtime = &pair, time.Time{}
	if st != nil {
		c.mtime = st.ModTime()
	}
	return c.loaded, nil
}
