package main

// Настройка CLI: адрес Jira в config.json, команды url / login / logout.
// Токен в файл не пишется — он в системном хранилище (см. client.go).

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const envURL = "JIRA_URL"

// errBrokenConfig — config.json есть, но это не JSON.
var errBrokenConfig = errors.New("файл настроек испорчен")

var errNoURL = errors.New("адрес Jira не задан: выполните `jira login` или `jira url АДРЕС`")

// builtinIssueType — тип новой задачи, пока пользователь не задал свой (`jira type`).
const builtinIssueType = "Task"

type config struct {
	URL  string `json:"url,omitempty"`
	Type string `json:"type,omitempty"` // тип новой задачи по умолчанию
}

// configPath — config.json в папке настроек ОС
// (macOS: ~/Library/Application Support/jira-cli, Linux: ~/.config/jira-cli, Windows: %AppData%\jira-cli).
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("папка настроек не найдена: %w", err)
	}
	return filepath.Join(dir, "jira-cli", "config.json"), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readConfig читает config.json; файла нет — пустая настройка без ошибки.
func readConfig(path string) (config, error) {
	var cfg config
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return config{}, fmt.Errorf("%w: %s (%v) — `jira url АДРЕС` запишет его заново", errBrokenConfig, path, err)
	}
	return cfg, nil
}

// saveURL записывает адрес в config.json. Испорченный файл заменяется новым с одним адресом:
// прочитать из него нечего, а `jira url АДРЕС` — способ его починить.
func saveURL(base string) error {
	err := updateConfig(func(cfg *config) { cfg.URL = base })
	if errors.Is(err, errBrokenConfig) {
		path, perr := configPath()
		if perr != nil {
			return perr
		}
		return writeConfig(path, config{URL: base})
	}
	return err
}

// updateConfig меняет одну настройку в config.json, сохраняя остальные.
// Файл, который не удалось прочитать (испорчен, нет прав), не трогает и возвращает ошибку.
func updateConfig(change func(*config)) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	cfg, err := readConfig(path)
	if err != nil {
		return err
	}
	change(&cfg)
	return writeConfig(path, cfg)
}

// writeConfig пишет файл целиком через временный и rename: прерванная запись не оставит обрезанный JSON.
func writeConfig(path string, cfg config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "config-*.json") // CreateTemp даёт права 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // после успешного rename — уже нечего удалять
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// normalizeURL приводит адрес Jira к виду scheme://host[/path] без хвостового "/".
// Без схемы подставляется https. Путь допустим: Jira DC бывает за контекстом (/jira).
func normalizeURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("пустой адрес")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%q — не адрес", s)
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%q: нужен адрес http:// или https://", s)
	case u.Hostname() == "":
		return "", fmt.Errorf("%q: в адресе нет хоста", s)
	case u.User != nil:
		return "", errors.New("логин и пароль в адресе не нужны: токен задаётся через `jira login`")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", fmt.Errorf("%q: адрес должен быть без ? и #", s)
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// jiraURL — действующий адрес Jira и его источник: JIRA_URL, затем config.json
// (source — "JIRA_URL" или путь файла). Адрес не задан — пустая строка без ошибки.
func jiraURL() (base, source string, err error) {
	if v := strings.TrimSpace(os.Getenv(envURL)); v != "" {
		base, err = normalizeURL(v)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", envURL, err)
		}
		return base, envURL, nil
	}
	path, err := configPath()
	if err != nil {
		return "", "", err
	}
	cfg, err := readConfig(path)
	if err != nil || cfg.URL == "" {
		return "", "", err
	}
	base, err = normalizeURL(cfg.URL) // файл могли править руками
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
	}
	return base, path, nil
}

// baseURL — действующий адрес Jira; не задан — errNoURL.
func baseURL() (string, error) {
	base, _, err := jiraURL()
	if err == nil && base == "" {
		err = errNoURL
	}
	return base, err
}

// envOverrideNote — предупреждение после сохранения адреса: JIRA_URL задана и указывает на другую Jira.
func envOverrideNote(saved string) string {
	v := strings.TrimSpace(os.Getenv(envURL))
	if v == "" {
		return ""
	}
	if n, err := normalizeURL(v); err == nil && n == saved {
		return ""
	}
	return fmt.Sprintf("внимание: задана переменная %s=%s — пока она есть, используется она, а не сохранённый адрес\n", envURL, v)
}

func cmdURL(_ *Client, a Args) (result, error) {
	if len(a.pos) == 0 {
		base, source, err := jiraURL()
		if err != nil {
			return nil, err
		}
		if base == "" {
			return nil, errNoURL
		}
		return message(base + "\t" + source + "\n"), nil
	}
	base, err := normalizeURL(a.pos[0])
	if err != nil {
		return nil, err
	}
	if err := saveURL(base); err != nil {
		return nil, fmt.Errorf("сохранение адреса: %w", err)
	}
	return message("ok: адрес сохранён: " + base + "\n" + envOverrideNote(base)), nil
}

// defaultIssueType — тип новой задачи для create без --type и его источник
// (путь config.json или "по умолчанию"). Папки настроек нет (CI без HOME, всё задано
// переменными) — действует встроенный тип: файл там и не предполагается.
func defaultIssueType() (typ, source string, err error) {
	path, err := configPath()
	if err != nil {
		return builtinIssueType, "по умолчанию", nil
	}
	cfg, err := readConfig(path)
	if err != nil {
		return "", "", err
	}
	if t := oneLine(cfg.Type); t != "" {
		return t, path, nil
	}
	return builtinIssueType, "по умолчанию", nil
}

func cmdType(_ *Client, a Args) (result, error) {
	if len(a.pos) == 0 {
		typ, source, err := defaultIssueType()
		if err != nil {
			return nil, err
		}
		return message(typ + "\t" + source + "\n"), nil
	}
	typ := oneLine(a.pos[0])
	if typ == "" {
		return nil, errors.New("пустой тип задачи")
	}
	if err := updateConfig(func(cfg *config) { cfg.Type = typ }); err != nil {
		return nil, fmt.Errorf("сохранение типа: %w", err)
	}
	return message("ok: тип задачи по умолчанию: " + typ + "\n"), nil
}

// loginIO — внешний мир команды login: ввод, куда писать вопросы, скрытие ввода токена
// (hide выключает эхо и возвращает функцию, которая его вернёт) и запись токена в хранилище.
type loginIO struct {
	in        *bufio.Reader
	prompt    io.Writer
	hide      func() (restore func())
	saveToken func(base, token string) error
}

func cmdLogin(_ *Client, a Args) (result, error) {
	hide := func() func() { return func() {} }
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		hide = func() func() {
			echo(false)
			return func() {
				echo(true)
				fmt.Fprintln(os.Stderr) // Enter пользователя не отобразился
			}
		}
	}
	return login(a, loginIO{in: bufio.NewReader(os.Stdin), prompt: os.Stderr, hide: hide, saveToken: saveToken})
}

// login — первая настройка: адрес (аргумент → действующий → вопрос), затем токен.
// Токен проверяется запросом /myself; адрес и токен сохраняются только после успеха.
func login(a Args, lio loginIO) (result, error) {
	var base string
	var err error
	newURL := false // адрес назван сейчас (аргументом или в ответ на вопрос) — его надо сохранить
	if len(a.pos) > 0 {
		if base, err = normalizeURL(a.pos[0]); err != nil {
			return nil, err
		}
		newURL = true
	} else if base, _, err = jiraURL(); err != nil {
		return nil, err
	} else if base == "" {
		fmt.Fprint(lio.prompt, "Адрес Jira (например https://jira.example.com): ")
		line, _ := lio.in.ReadString('\n')
		if base, err = normalizeURL(line); err != nil {
			return nil, err
		}
		newURL = true
	}

	fmt.Fprintf(lio.prompt, "Personal Access Token для %s (Профиль → Personal Access Tokens): ", base)
	restore := lio.hide()
	line, rerr := lio.in.ReadString('\n')
	restore()
	token := strings.TrimSpace(line)
	if token == "" {
		if rerr != nil && rerr != io.EOF {
			return nil, rerr
		}
		return nil, errors.New("пустой токен")
	}

	u, err := newClientWithToken(base, token).myself()
	if err != nil {
		return nil, fmt.Errorf("проверка токена: %w", err)
	}
	who := fmt.Sprintf("%s (%s) на %s", oneLine(u.Name), oneLine(u.DisplayName), base)
	note := ""
	if newURL {
		if err := saveURL(base); err != nil {
			return nil, fmt.Errorf("сохранение адреса: %w", err)
		}
		note = envOverrideNote(base)
	}
	if err := lio.saveToken(base, token); err != nil {
		err = fmt.Errorf("сохранение токена: %w", err)
		if newURL {
			return message("токен верный: " + who + "; адрес сохранён\n" + note), err
		}
		return nil, err
	}
	return message("ok: " + who + ", токен сохранён\n" + note), nil
}

// deleteTokenCmd собирает команду удаления токена из системного хранилища.
func deleteTokenCmd(goos, base string) (*exec.Cmd, error) {
	acct := accountName(base)
	switch goos {
	case "darwin":
		return exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", acct), nil
	case "linux":
		return exec.Command("secret-tool", "clear", "service", keychainService, "account", acct), nil
	}
	return nil, errNoTokenStore
}

// deleteToken удаляет токен из системного хранилища.
func deleteToken(base string) error {
	if runtime.GOOS == "windows" {
		return osDeleteToken(credTarget(base))
	}
	cmd, err := deleteTokenCmd(runtime.GOOS, base)
	if err != nil {
		return err
	}
	err = cmd.Run()
	var exit *exec.ExitError
	if goosIsDarwin && errors.As(err, &exit) && exit.ExitCode() == securityNotFound {
		return errTokenNotFound
	}
	return err
}

// securityNotFound — код выхода `security`, когда записи в Keychain нет (errSecItemNotFound).
const securityNotFound = 44

const goosIsDarwin = runtime.GOOS == "darwin"

func cmdLogout(_ *Client, a Args) (result, error) {
	base, err := baseURL()
	if err != nil {
		return nil, err
	}
	acct := accountName(base)
	switch err := deleteToken(base); {
	case errors.Is(err, errNoTokenStore):
		return nil, err
	case errors.Is(err, errTokenNotFound):
		return nil, fmt.Errorf("токена для %s в системном хранилище нет", acct)
	case err != nil:
		return nil, fmt.Errorf("токен для %s не удалён: %w", acct, err)
	}
	msg := "ok: токен для " + acct + " удалён\n"
	if strings.TrimSpace(os.Getenv("JIRA_TOKEN")) != "" {
		msg += "внимание: переменная JIRA_TOKEN задана — команды по-прежнему работают с ней\n"
	}
	return message(msg), nil
}
