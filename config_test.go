package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempConfig уводит папку настроек во временный каталог и снимает JIRA_URL:
// тесты не читают и не портят настоящий config.json.
func tempConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("JIRA_URL", "")
}

func runNoClient(t *testing.T, cmd string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := run(&buf, cmd, args, func() (*Client, error) {
		t.Fatalf("%s не должна создавать клиент", cmd)
		return nil, nil
	})
	return buf.String(), err
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://jira.example.com":       "https://jira.example.com",
		"https://jira.example.com/":      "https://jira.example.com",
		"  https://jira.example.com/ \n": "https://jira.example.com",
		"jira.example.com":               "https://jira.example.com",
		"http://localhost:8080/jira/":    "http://localhost:8080/jira",
		"HTTPS://Jira.Example.com":       "https://jira.example.com",
	} {
		got, err := normalizeURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "ftp://jira.example.com", "https://", "https://user:pw@jira.example.com",
		"https://jira.example.com/?a=1", "https://jira.example.com/#x", "https://jira example.com",
	} {
		if got, err := normalizeURL(in); err == nil {
			t.Errorf("normalizeURL(%q) = %q, ожидалась ошибка", in, got)
		}
	}
}

func TestURLCommandSavesAndShows(t *testing.T) {
	tempConfig(t)
	if out, err := runNoClient(t, "url"); err == nil || !strings.Contains(err.Error(), "jira login") {
		t.Fatalf("без адреса: out = %q, err = %v; ожидалась подсказка про jira login", out, err)
	}
	out, err := runNoClient(t, "url", "jira.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ok: адрес сохранён: https://jira.example.com\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	out, err = runNoClient(t, "url")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://jira.example.com\t" + path + "\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("config.json доступен другим: %v", perm)
	}
}

func TestURLCommandRejectsBadURL(t *testing.T) {
	tempConfig(t)
	for _, bad := range []string{"", "ftp://x", "https://a/?q=1"} {
		if out, err := runNoClient(t, "url", bad); err == nil {
			t.Errorf("url %q: out = %q, ожидалась ошибка", bad, out)
		}
	}
	if path, _ := configPath(); fileExists(path) {
		t.Error("неверный адрес не должен создавать config.json")
	}
}

func TestEnvURLWinsOverConfig(t *testing.T) {
	tempConfig(t)
	if _, err := runNoClient(t, "url", "https://saved.example.com"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_URL", "https://env.example.com/")
	out, err := runNoClient(t, "url")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://env.example.com\tJIRA_URL\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	// сохранение при заданной JIRA_URL предупреждает, что переменная важнее
	out, err = runNoClient(t, "url", "https://other.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "JIRA_URL") {
		t.Errorf("нет предупреждения про JIRA_URL: %q", out)
	}
	t.Setenv("JIRA_URL", "not a url")
	if _, err := baseURL(); err == nil || !strings.Contains(err.Error(), "JIRA_URL") {
		t.Errorf("неверная JIRA_URL: err = %v", err)
	}
}

func TestNewClientWithoutURL(t *testing.T) {
	tempConfig(t)
	t.Setenv("JIRA_TOKEN", "tok")
	if _, err := newClient(); !errors.Is(err, errNoURL) {
		t.Fatalf("err = %v, want errNoURL", err)
	}
}

func TestBrokenConfigIsReported(t *testing.T) {
	tempConfig(t)
	path, _ := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := baseURL(); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, ожидалась ошибка с путём файла", err)
	}
	// jira url АДРЕС чинит испорченный файл
	if _, err := runNoClient(t, "url", "https://jira.example.com"); err != nil {
		t.Fatal(err)
	}
	if got, err := baseURL(); err != nil || got != "https://jira.example.com" {
		t.Errorf("baseURL = %q, %v", got, err)
	}
}

// ---------- login ----------

type loginRun struct {
	out     string
	prompts string
	saved   map[string]string // base → token
	err     error
}

func runLogin(stdin string, saveErr error, args ...string) loginRun {
	var prompts bytes.Buffer
	r := loginRun{saved: map[string]string{}}
	res, err := login(Args{pos: args}, loginIO{
		in:     bufio.NewReader(strings.NewReader(stdin)),
		prompt: &prompts,
		hide:   func() func() { return func() {} },
		saveToken: func(base, token string) error {
			if saveErr != nil {
				return saveErr
			}
			r.saved[base] = token
			return nil
		},
	})
	if res != nil {
		var buf bytes.Buffer
		res.text(&buf)
		r.out = buf.String()
	}
	r.prompts, r.err = prompts.String(), err
	return r
}

var myselfOK = routes{"GET /rest/api/2/myself": {200, `{"name":"u","displayName":"U"}`}}

func TestLoginAsksURLWhenNotConfigured(t *testing.T) {
	tempConfig(t)
	fj := newFakeJira(t, myselfOK)
	r := runLogin(fj.srv.URL+"/\nsecret\n", nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !strings.Contains(r.prompts, "Адрес Jira") || !strings.Contains(r.prompts, "Personal Access Token") {
		t.Errorf("prompts = %q", r.prompts)
	}
	if want := "ok: u (U) на " + fj.srv.URL + ", токен сохранён\n"; r.out != want {
		t.Errorf("out = %q, want %q", r.out, want)
	}
	if r.saved[fj.srv.URL] != "secret" {
		t.Errorf("saved = %v", r.saved)
	}
	if got, err := baseURL(); err != nil || got != fj.srv.URL {
		t.Errorf("baseURL = %q, %v; адрес должен сохраниться", got, err)
	}
	if len(fj.requests) != 1 || fj.requests[0].Auth != "Bearer secret" {
		t.Errorf("requests = %+v", fj.requests)
	}
}

func TestLoginUsesConfiguredURL(t *testing.T) {
	tempConfig(t)
	fj := newFakeJira(t, myselfOK)
	if err := saveURL(fj.srv.URL); err != nil {
		t.Fatal(err)
	}
	r := runLogin("secret\n", nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.Contains(r.prompts, "Адрес Jira") {
		t.Errorf("адрес уже задан, спрашивать не нужно: %q", r.prompts)
	}
	if r.saved[fj.srv.URL] != "secret" {
		t.Errorf("saved = %v", r.saved)
	}
}

func TestLoginURLArgumentReplacesConfigured(t *testing.T) {
	tempConfig(t)
	fj := newFakeJira(t, myselfOK)
	if err := saveURL("https://old.example.com"); err != nil {
		t.Fatal(err)
	}
	r := runLogin("secret\n", nil, fj.srv.URL)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got, _ := baseURL(); got != fj.srv.URL {
		t.Errorf("baseURL = %q, want %q", got, fj.srv.URL)
	}
}

// Адрес и токен сохраняются только после успешной проверки токена.
func TestLoginSavesNothingWhenTokenRejected(t *testing.T) {
	tempConfig(t)
	fj := newFakeJira(t, routes{"GET /rest/api/2/myself": {401, `{"errorMessages":["нет доступа"]}`}})
	r := runLogin("bad\n", nil, fj.srv.URL)
	if r.err == nil || !strings.Contains(r.err.Error(), "проверка токена") {
		t.Fatalf("err = %v", r.err)
	}
	if len(r.saved) != 0 {
		t.Errorf("токен сохранён: %v", r.saved)
	}
	if _, err := baseURL(); !errors.Is(err, errNoURL) {
		t.Errorf("адрес сохранён при неверном токене: %v", err)
	}
}

func TestLoginRejectsEmptyInput(t *testing.T) {
	tempConfig(t)
	if r := runLogin("\n", nil); r.err == nil {
		t.Error("пустой адрес: ожидалась ошибка")
	}
	if r := runLogin("\n", nil, "https://jira.example.com"); r.err == nil || !strings.Contains(r.err.Error(), "пустой токен") {
		t.Errorf("пустой токен: err = %v", r.err)
	}
	if r := runLogin("", nil, "ftp://x"); r.err == nil || r.prompts != "" {
		t.Errorf("неверный адрес должен падать до вопросов: err = %v, prompts = %q", r.err, r.prompts)
	}
}

// Запись токена в хранилище не удалась: адрес уже сохранён, об этом сказано, ошибка про токен возвращается.
func TestLoginKeepsURLWhenTokenStoreFails(t *testing.T) {
	tempConfig(t)
	fj := newFakeJira(t, myselfOK)
	r := runLogin("secret\n", errors.New("хранилища нет"), fj.srv.URL)
	if r.err == nil || !strings.Contains(r.err.Error(), "хранилища нет") {
		t.Fatalf("err = %v", r.err)
	}
	if !strings.Contains(r.out, "адрес сохранён") {
		t.Errorf("out = %q", r.out)
	}
	if got, _ := baseURL(); got != fj.srv.URL {
		t.Errorf("baseURL = %q", got)
	}
}

// ---------- logout ----------

func TestDeleteTokenCmd(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin": "security delete-generic-password -s jira-cli -a sd.example",
		"linux":  "secret-tool clear service jira-cli account sd.example",
	} {
		cmd, err := deleteTokenCmd(goos, "https://sd.example")
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if got := strings.Join(cmd.Args, " "); got != want {
			t.Errorf("%s: args = %q, want %q", goos, got, want)
		}
	}
	if _, err := deleteTokenCmd("plan9", "https://sd.example"); err == nil {
		t.Error("plan9: ожидалась ошибка")
	}
}

func TestLogoutNeedsURL(t *testing.T) {
	tempConfig(t)
	if _, err := runNoClient(t, "logout"); !errors.Is(err, errNoURL) {
		t.Fatalf("err = %v, want errNoURL", err)
	}
}
