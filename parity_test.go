package main

// Паритет text/JSON: однострочные поля (key, status, type, priority, имена, summary) в тексте —
// в одну строку (oneLine), в JSON — сырые строки с сервера.

import (
	"encoding/json"
	"testing"
	"time"
)

// decodeJSON разбирает вывод --json в map (сравниваем декодированные значения, не байты).
func decodeJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, out)
	}
	return got
}

// runBoth выполняет команду в тексте и с --json на одном фейке.
func runBoth(t *testing.T, rs routes, cmd string, args ...string) (text string, js map[string]any) {
	t.Helper()
	fj := newFakeJira(t, rs)
	text, err := fj.run(cmd, args...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := fj.run(cmd, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	return text, decodeJSON(t, out)
}

// wantFields сверяет строковые поля объекта JSON.
func wantFields(t *testing.T, where string, obj any, want map[string]string) {
	t.Helper()
	m, ok := obj.(map[string]any)
	if !ok {
		t.Fatalf("%s: не объект: %v", where, obj)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s.%s = %q, want %q", where, k, m[k], v)
		}
	}
}

func TestMeParityMultilineNames(t *testing.T) {
	text, js := runBoth(t, routes{
		"GET /rest/api/2/myself": {200, `{"name":"nizom\nx","displayName":"Низом\r\nН."}`},
	}, "me")
	if want := "nizom x\tНизом Н.\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	wantFields(t, "me", js, map[string]string{"name": "nizom\nx", "displayName": "Низом\r\nН."})
}

const multilineSearch = `{"total":1,"issues":[{"key":"K-1\nX","fields":{"summary":"a\nb\tc",
	"status":{"name":"В\nработе"},"timetracking":{"originalEstimateSeconds":3600}}}]}`

func TestListParityMultilineFields(t *testing.T) {
	for _, args := range [][]string{{"ls"}, {"q", "project = K"}} {
		text, js := runBoth(t, routes{"GET /rest/api/2/search": {200, multilineSearch}}, args[0], args[1:]...)
		if want := "K-1 X\tВ работе\t1h\t-\ta b c\n"; text != want {
			t.Errorf("%v: text = %q, want %q", args, text, want)
		}
		issues, _ := js["issues"].([]any)
		if len(issues) != 1 {
			t.Fatalf("%v: issues = %v", args, js["issues"])
		}
		wantFields(t, args[0]+".issues[0]", issues[0],
			map[string]string{"key": "K-1\nX", "status": "В\nработе", "summary": "a\nb\tc"})
	}
}

const multilineShow = `{"key":"K-1\nX","fields":{
	"summary":"заголовок\nassignee: root","status":{"name":"В\nработе"},"issuetype":{"name":"За\nдача"},
	"priority":{"name":"High\n"},"assignee":{"name":"bob\nx"},"reporter":{"name":"eve\r\ny"},
	"description":"строка1\nстрока2",
	"parent":{"key":"K-0\nY","fields":{"summary":"родитель\nparent: K-9"}},
	"comment":{"total":1,"comments":[{"author":{"name":"ann\nz"},"created":"2026-11-10T10:00:00.000+0500","body":"много\nстрок"}]}
}}`

func TestShowParityMultilineFields(t *testing.T) {
	text, js := runBoth(t, routes{"GET /rest/api/2/issue/K-1": {200, multilineShow}}, "show", "K-1")
	want := "K-1 X [За дача] В работе · High\nзаголовок assignee: root\n" +
		"assignee: bob x  reporter: eve y\n" +
		"estimate: -  spent: -  remaining: -\n" +
		"created: -  updated: -\n" +
		"parent: K-0 Y родитель parent: K-9\n" +
		"\nстрока1\nстрока2\n" +
		"\nкомментарии (1 из 1):\n- ann z 2026-11-10: много\nстрок\n"
	if text != want {
		t.Errorf("text = %q\nwant   %q", text, want)
	}
	wantFields(t, "show", js, map[string]string{"key": "K-1\nX", "type": "За\nдача", "status": "В\nработе",
		"priority": "High\n", "summary": "заголовок\nassignee: root", "assignee": "bob\nx",
		"reporter": "eve\r\ny", "description": "строка1\nстрока2"})
	wantFields(t, "show.parent", js["parent"], map[string]string{"key": "K-0\nY", "summary": "родитель\nparent: K-9"})
	comments, _ := js["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("comments = %v", js["comments"])
	}
	wantFields(t, "show.comments[0]", comments[0], map[string]string{"author": "ann\nz", "body": "много\nстрок"})
}

// Пользователь отчёта — из /myself, с \n в имени; задача закрыта им и со списанием в окне.
var multilineReport = routes{
	"GET /rest/api/2/myself": {200, `{"name":"u\nx","displayName":"U"}`},
	"GET /rest/api/2/search": {200, `{"total":1,"issues":[{"key":"K-1\nX","fields":{
		"summary":"a\nb","status":{"name":"В\nработе"},"assignee":{"name":"u\nx"},
		"resolutiondate":"2026-10-15T12:00:00.000+0000","timeoriginalestimate":3600,
		"worklog":{"total":1,"worklogs":[{"author":{"name":"u\nx"},"started":"2026-10-14T12:00:00.000+0000","timeSpentSeconds":1800}]}}}]}`},
}

// reportInUTC: окно отчёта — в time.Local; фиксируем UTC, чтобы даты в expected не зависели от машины.
func reportInUTC(t *testing.T) { inLocal(t, time.UTC) }

// inLocal — пояс пользователя в тесте: time.Local = loc до конца теста.
func inLocal(t *testing.T, loc *time.Location) {
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func TestReportParityMultilineFields(t *testing.T) {
	reportInUTC(t)
	text, js := runBoth(t, multilineReport, "report", "2026-10")
	want := "# 2026-10 · u x · KEY ОЦЕНКА СПИСАНО ЗАКРЫТА STATUS SUMMARY\n" +
		"K-1 X\t1h\t30m\t2026-10-15\tВ работе\ta b\n" +
		"# итого: оценка закрытых 1h · списано 30m · задач 1, закрыто 1\n"
	if text != want {
		t.Errorf("text = %q\nwant   %q", text, want)
	}
	wantFields(t, "report", js, map[string]string{"user": "u\nx", "month": "2026-10"})
	issues, _ := js["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v", js["issues"])
	}
	wantFields(t, "report.issues[0]", issues[0],
		map[string]string{"key": "K-1\nX", "status": "В\nработе", "summary": "a\nb"})
}

func TestReportByDayParityMultilineFields(t *testing.T) {
	reportInUTC(t)
	text, js := runBoth(t, multilineReport, "report", "2026-10", "--by", "day")
	want := "# 2026-10 · u x · списано по дням\n" +
		"2026-10-14\t30m\n" +
		"# итого списано: 30m\n"
	if text != want {
		t.Errorf("text = %q\nwant   %q", text, want)
	}
	wantFields(t, "report --by day", js, map[string]string{"user": "u\nx", "month": "2026-10"})
}

func TestCreateKeepsServerKeyOnOneLine(t *testing.T) {
	fj := newFakeJira(t, routes{"POST /rest/api/2/issue": {201, `{"key":"K-2\nX"}`}})
	out, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if want := "created K-2 X " + fj.srv.URL + "/browse/K-2 X\n"; out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

// Команды без JSON: однострочные поля с сервера (имена переходов и статусов) — тоже в одну строку.
const multilineTransitions = `{"transitions":[{"id":"11","name":"В\nработу","to":{"name":"В\nработе"}},
	{"id":"21","name":"Готово","to":{"name":"Закрыта\nK-1 → x"}}]}`

func TestMoveKeepsSingleLineFieldsOnOneLine(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"K-1"}, "В работу\t→ В работе\nГотово\t→ Закрыта K-1 → x\n"},
		{[]string{"K-1", "готово"}, "K-1 → Закрыта K-1 → x\n"},
	} {
		fj := newFakeJira(t, routes{
			"GET /rest/api/2/issue/K-1/transitions":  {200, multilineTransitions},
			"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		})
		out, err := fj.run("move", tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		if out != tc.want {
			t.Errorf("%v: out = %q, want %q", tc.args, out, tc.want)
		}
	}
}
