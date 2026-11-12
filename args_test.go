package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Разбор аргументов по спецификации команды: ошибка — до любого запроса к Jira.
func TestArgsRejectedBeforeJira(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // подстрока ошибки
	}{
		{[]string{"create", "--project", "K", "--summary", "S", "--yes=false"}, "--yes: значение не принимает"},
		{[]string{"ls", "--all=0"}, "--all: значение не принимает"},
		{[]string{"ls", "--user"}, "--user: нужно значение"},
		{[]string{"log", "K-1", "2h", "--date"}, "--date: нужно значение"},
		{[]string{"ls", "--user", "a", "--user", "b"}, "--user: указан дважды"},
		{[]string{"ls", "-n", "1", "--n=2"}, "-n: указан дважды"},
		{[]string{"comment", "K-1", "-1", "баг"}, "неизвестный флаг -1 (см. jira help; текст с «-» в начале — после --)"},
		{[]string{"show", "K-1", "--yes"}, "неизвестный флаг --yes"},
		{[]string{"ls", "PROJ"}, `лишний аргумент "PROJ" (использование: jira ls [--all] [--user U] [--project P] [-n 30])`},
		{[]string{"assign", "K-1", "ivan", "petrov"}, `лишний аргумент "petrov"`},
		{[]string{"show", "K-1", "K-2"}, `лишний аргумент "K-2"`},
		{[]string{"report", "2026-10", "2026-11"}, `лишний аргумент "2026-11"`},
		{[]string{"me", "x"}, `лишний аргумент "x" (использование: jira me)`},
		{[]string{"show"}, "использование: jira show KEY [--comments 3] [--full]"},
		{[]string{"assign", "K-1"}, "использование: jira assign KEY U|me|none [--yes]"},
		{[]string{"assign", "K-1", "", "--yes"}, "использование: jira assign KEY U|me|none [--yes]"}, // "" — не me
		{[]string{"show", ""}, "использование: jira show KEY"},
		{[]string{"log", " ", "2h"}, "использование: jira log KEY"},
		{[]string{"log", "K-1", "--date", "2026-11-10"}, "использование: jira log KEY 1d 4h [комментарий] [--date YYYY-MM-DD]"},
		{[]string{"comment", "K-1"}, "использование: jira comment KEY текст|-"},
		{[]string{"q"}, "использование: jira q 'JQL' [-n 30]"},
		{[]string{"create", "x"}, "использование: jira create --project P --summary S [--type T] [--desc D|-] [--estimate 4h] [--assignee U|me|none] [--yes]"},
		// пустое значение флага — не «по умолчанию»: "" не me, не «без фильтра», не «сегодня»
		{[]string{"create", "--project", "K", "--summary", "S", "--assignee", "", "--yes"}, "--assignee: пустое значение"},
		{[]string{"create", "--project", "K", "--summary", "S", "--desc", " "}, "--desc: пустое значение"},
		{[]string{"ls", "--project="}, "--project: пустое значение"},
		{[]string{"ls", "--user", ""}, "--user: пустое значение"},
		{[]string{"log", "K-1", "2h", "--date", ""}, "--date: пустое значение"},
		{[]string{"q", "x", "-n", ""}, "-n: пустое значение"},
		// неверное значение флага — до первого запроса
		{[]string{"ls", "--user", "ivan", "-n", "0"}, "-n: ожидается положительное число"},
		{[]string{"q", "x", "-n", "abc"}, "-n: ожидается положительное число"},
		{[]string{"show", "K-1", "--comments", "x"}, "--comments: ожидается число"},
		{[]string{"report", "--by", "week", "--user", "u"}, "--by: только day"},
		{[]string{"report", "2026-13", "--user", "u"}, "месяц: ожидается YYYY-MM"},
		{[]string{"log", "K-1", "2h", "--date", "2026-13-01"}, "--date: ожидается YYYY-MM-DD"},
		{[]string{"create", "--project", "K", "--summary", "S", "--estimate", "abc"}, "--estimate:"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			fj := newFakeJira(t, routes{})
			out, err := fj.run(tc.args[0], tc.args[1:]...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if out != "" || len(fj.requests) != 0 {
				t.Fatalf("out = %q, requests = %+v", out, fj.requests)
			}
		})
	}
}

// Допустимые формы аргументов доходят до команды как раньше.
func TestArgsAccepted(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // запрос к Jira: "МЕТОД путь тело"
	}{
		{[]string{"comment", "K-1", "--", "-1", "баг"}, `POST /rest/api/2/issue/K-1/comment {"body":"-1 баг"}`},
		{[]string{"comment", "--", "K-1", "--yes"}, `POST /rest/api/2/issue/K-1/comment {"body":"--yes"}`},
		{[]string{"log", "--date=2026-11-10", "K-1", "30m"}, `POST /rest/api/2/issue/K-1/worklog {"started":"2026-11-10T09:00:00.000+0000","timeSpent":"30m"}`},
		{[]string{"q", "--n", "3", "project = K"}, "GET /rest/api/2/search?fields=summary%2Cstatus%2Ctimetracking&jql=project+%3D+K&maxResults=3&startAt=0 "},
		{[]string{"q", "project = K", "-n=3"}, "GET /rest/api/2/search?fields=summary%2Cstatus%2Ctimetracking&jql=project+%3D+K&maxResults=3&startAt=0 "},
		{[]string{"assign", "-yes", "K-1", "none"}, `PUT /rest/api/2/issue/K-1/assignee {"name":null}`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			reportInUTC(t)
			fj := newFakeJira(t, shapeRoutes)
			if _, err := fj.run(tc.args[0], tc.args[1:]...); err != nil {
				t.Fatal(err)
			}
			if len(fj.requests) != 1 {
				t.Fatalf("requests = %+v", fj.requests)
			}
			r := fj.requests[0]
			if got := r.Method + " " + r.Path + " " + r.Body; got != tc.want {
				t.Fatalf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// docFlag — флаг в строке использования: "--user U" — со значением, "[--all]" — булев.
var docFlag = regexp.MustCompile(`(?:^|[\s\[])--?([a-z]+)( [^\s\[\]-][^\s\]]*)?`)

// docFlags — флаги строки использования: имя → со значением.
func docFlags(line string) flagSpec {
	s := flagSpec{}
	for _, m := range docFlag.FindAllStringSubmatch(line, -1) {
		s[m[1]] = m[2] != ""
	}
	return s
}

// Флаги в usage у каждой команды — ровно флаги её спецификации (без --json), с той же арностью.
func TestUsageMatchesCommandSpec(t *testing.T) {
	for name, cmd := range commands {
		syn := synopsis(name)
		if syn == "" {
			t.Errorf("%s: нет в usage", name)
			continue
		}
		if got := docFlags(syn); !reflect.DeepEqual(got, cmd.flags) && !(len(got) == 0 && len(cmd.flags) == 0) {
			t.Errorf("%s: в usage %v, в спецификации %v", name, got, cmd.flags)
		}
	}
}

// SKILL.md может упоминать не все флаги, но каждый упомянутый должен быть в спецификации команды.
func TestSkillFlagsMatchCommandSpec(t *testing.T) {
	b, err := os.ReadFile("skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "jira ")
		if !ok {
			continue
		}
		rest, _, _ = strings.Cut(rest, "#")
		name, _, _ := strings.Cut(rest, " ")
		cmd, ok := commands[name]
		if !ok {
			t.Errorf("SKILL.md: неизвестная команда %q", name)
			continue
		}
		n++
		for f, withValue := range docFlags(rest) {
			if spec, known := cmd.flags[f]; !known || spec != withValue {
				t.Errorf("SKILL.md: %s: флаг %s (со значением: %v) не совпадает со спецификацией %v", name, flagName(f), withValue, cmd.flags)
			}
		}
	}
	if n == 0 {
		t.Fatal("SKILL.md: не найдено ни одной строки jira …")
	}
}
