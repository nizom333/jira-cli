package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Тип новой задачи по умолчанию: Task, пока пользователь не задал свой командой jira type.
func TestTypeCommandShowsAndSavesDefaultIssueType(t *testing.T) {
	tempConfig(t)
	out, err := runNoClient(t, "type")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Task\tпо умолчанию\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	out, err = runNoClient(t, "type", " Задача ")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ok: тип задачи по умолчанию: Задача\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	path, _ := configPath()
	if out, _ = runNoClient(t, "type"); out != "Задача\t"+path+"\n" {
		t.Errorf("out = %q", out)
	}
	if _, err := runNoClient(t, "type", "  "); err == nil {
		t.Error("пустой тип: ожидалась ошибка")
	}
}

// Адрес и тип лежат в одном файле: сохранение одного не стирает другое.
func TestURLAndTypeDoNotOverwriteEachOther(t *testing.T) {
	tempConfig(t)
	if _, err := runNoClient(t, "url", "https://jira.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := runNoClient(t, "type", "Bug"); err != nil {
		t.Fatal(err)
	}
	if _, err := runNoClient(t, "url", "https://other.example.com"); err != nil {
		t.Fatal(err)
	}
	if got, err := baseURL(); err != nil || got != "https://other.example.com" {
		t.Errorf("baseURL = %q, %v", got, err)
	}
	if out, _ := runNoClient(t, "type"); !strings.HasPrefix(out, "Bug\t") {
		t.Errorf("тип потерян: %q", out)
	}
}

func TestCreateUsesDefaultIssueType(t *testing.T) {
	for _, tc := range []struct {
		saved, flag, want string
	}{
		{"", "", "Task"},
		{"Задача", "", "Задача"},
		{"Задача", "Bug", "Bug"},
	} {
		fj := newFakeJira(t, routes{"POST /rest/api/2/issue": {201, `{"key":"K-2"}`}})
		if tc.saved != "" {
			if _, err := runNoClient(t, "type", tc.saved); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"--project", "K", "--summary", "S", "--assignee", "none", "--yes"}
		if tc.flag != "" {
			args = append(args, "--type", tc.flag)
		}
		if _, err := fj.run("create", args...); err != nil {
			t.Fatal(err)
		}
		if body := fj.requests[0].Body; !strings.Contains(body, `"issuetype":{"name":"`+tc.want+`"}`) {
			t.Errorf("saved=%q flag=%q: body = %s", tc.saved, tc.flag, body)
		}
	}
}

// Файл настроек, который не удалось прочитать, jira type не перезаписывает: в нём может быть адрес.
func TestTypeDoesNotOverwriteUnreadableConfig(t *testing.T) {
	for name, damage := range map[string]func(path string) error{
		"не JSON": func(path string) error { return os.WriteFile(path, []byte("{oops"), 0o600) },
		"не файл": func(path string) error { return os.Mkdir(path, 0o700) },
	} {
		tempConfig(t)
		path, err := configPath()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := damage(path); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		if out, err := runNoClient(t, "type", "Bug"); err == nil {
			t.Errorf("%s: out = %q, ожидалась ошибка", name, out)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("%s: файл изменён: %q", name, after)
		}
	}
}

// Всё задано переменными, папки настроек нет (CI без HOME): create берёт встроенный тип.
func TestCreateWithoutConfigDirUsesBuiltinType(t *testing.T) {
	fj := newFakeJira(t, routes{"POST /rest/api/2/issue": {201, `{"key":"K-2"}`}})
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(k, "")
	}
	if _, err := configPath(); err == nil {
		t.Skip("на этой ОС папка настроек находится и без HOME")
	}
	if _, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--yes"); err != nil {
		t.Fatal(err)
	}
	if body := fj.requests[0].Body; !strings.Contains(body, `"issuetype":{"name":"Task"}`) {
		t.Errorf("body = %s", body)
	}
}
