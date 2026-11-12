package main

// Тестовый каркас: команды запускаются через run() против httptest-фейка Jira, без сети.
//
//	fj := newFakeJira(t, routes{
//		"GET /rest/api/2/myself":      {200, `{"name":"u","displayName":"U"}`},
//		"PUT /rest/api/2/issue/K-1":   {204, ""},
//		"GET /rest/api/2/search?jql=…": {200, `…`}, // ключ с "?" сверяется с query целиком
//	})
//	out, err := fj.run("log", "K-1", "2h", "--date", "2026-11-10") // stdout команды и её ошибка
//	fj.requests // []request{Method, Path (с query), Body, Auth} — в порядке поступления
//
// Ключ маршрута — "МЕТОД путь"; без "?" query при сопоставлении игнорируется.
// Запрос без маршрута отвечает 404 и валит тест. Клиент создаётся newClientWithToken
// с токеном fakeToken; JIRA_TOKEN/Keychain не читаются.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const fakeToken = "test-token"

// reply — ответ фейка: HTTP-статус и тело (JSON как строка).
type reply struct {
	status int
	body   string
}

type routes map[string]reply

// request — запрос, пришедший в фейк.
type request struct {
	Method string
	Path   string // путь + "?" + query, если есть
	Body   string
	Auth   string // заголовок Authorization
}

type fakeJira struct {
	t        *testing.T
	srv      *httptest.Server
	routes   routes
	mu       sync.Mutex
	requests []request
}

func newFakeJira(t *testing.T, rs routes) *fakeJira {
	t.Helper()
	tempConfig(t) // команды не читают настоящий config.json (адрес, тип задачи по умолчанию)
	fj := &fakeJira{t: t, routes: rs}
	fj.srv = httptest.NewServer(http.HandlerFunc(fj.serve))
	t.Cleanup(fj.srv.Close)
	return fj
}

func (fj *fakeJira) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	full := r.URL.Path
	if r.URL.RawQuery != "" {
		full += "?" + r.URL.RawQuery
	}
	fj.mu.Lock()
	fj.requests = append(fj.requests, request{Method: r.Method, Path: full, Body: string(body), Auth: r.Header.Get("Authorization")})
	fj.mu.Unlock()

	rep, ok := fj.routes[r.Method+" "+full]
	if !ok {
		rep, ok = fj.routes[r.Method+" "+r.URL.Path]
	}
	if !ok {
		fj.t.Errorf("фейк Jira: нет маршрута для %s %s", r.Method, full)
		rep = reply{404, `{"errorMessages":["fake: нет маршрута"]}`}
	}
	if rep.body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(rep.status)
	_, _ = io.WriteString(w, rep.body)
}

// run выполняет команду так же, как main (аргументы разбираются по спецификации команды), и возвращает её stdout.
func (fj *fakeJira) run(cmd string, args ...string) (string, error) {
	fj.t.Helper()
	var buf bytes.Buffer
	connect := func() (*Client, error) { return newClientWithToken(strings.TrimRight(fj.srv.URL, "/"), fakeToken), nil }
	err := run(&buf, cmd, args, connect)
	return buf.String(), err
}
