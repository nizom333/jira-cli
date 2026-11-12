package main

import (
	"strings"
	"testing"
)

func countMethod(fj *fakeJira, method string) int {
	n := 0
	for _, r := range fj.requests {
		if r.Method == method {
			n++
		}
	}
	return n
}

func TestCreatePreviewShowsResolvedAssignee(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=ivan.ivanov": {200, `{"name":"ivan.ivanov","displayName":"Ivan Ivanov"}`},
	})
	out, err := fj.run("create", "--project", "P", "--summary", "S", "--assignee", "ivan.ivanov")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assignee: ivan.ivanov (Ivan Ivanov)\n") {
		t.Fatalf("out = %q", out)
	}
	if n := countMethod(fj, "POST"); n != 0 {
		t.Fatalf("POST-запросов: %d", n)
	}
}

func TestCreateUnknownAssigneeFailsOnPreview(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=nobody": {404, `{"errorMessages":["The user named 'nobody' does not exist"]}`},
	})
	_, err := fj.run("create", "--project", "P", "--summary", "S", "--assignee", "nobody")
	if err == nil || err.Error() != "пользователь nobody не найден" {
		t.Fatalf("err = %v", err)
	}
	if n := countMethod(fj, "POST"); n != 0 {
		t.Fatalf("POST-запросов: %d", n)
	}
}

func TestCreateDefaultsAssigneeToMyself(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/myself": {200, `{"name":"nizom","displayName":"Низом Н."}`},
	})
	out, err := fj.run("create", "--project", "P", "--summary", "S")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assignee: nizom (Низом Н.)\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestCreateAssigneeNone(t *testing.T) {
	fj := newFakeJira(t, routes{})
	out, err := fj.run("create", "--project", "P", "--summary", "S", "--assignee", "none")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "assignee: none\n") {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 0 {
		t.Fatalf("requests = %+v", fj.requests)
	}

	fj = newFakeJira(t, routes{
		"POST /rest/api/2/issue": {201, `{"key":"P-1"}`},
	})
	if _, err := fj.run("create", "--project", "P", "--summary", "S", "--assignee", "none", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(fj.requests) != 1 || fj.requests[0].Method != "POST" {
		t.Fatalf("requests = %+v", fj.requests)
	}
	if strings.Contains(fj.requests[0].Body, "assignee") {
		t.Fatalf("в POST есть assignee: %s", fj.requests[0].Body)
	}
}

func TestAssignMeYesPutsMyselfName(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/myself":             {200, `{"name":"nizom","displayName":"Низом Н."}`},
		"PUT /rest/api/2/issue/K-1/assignee": {204, ""},
	})
	out, err := fj.run("assign", "K-1", "me", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if out != "K-1: assignee → nizom (Низом Н.)\n" {
		t.Fatalf("out = %q", out)
	}
	last := fj.requests[len(fj.requests)-1]
	if last.Method != "PUT" || last.Body != `{"name":"nizom"}` {
		t.Fatalf("request = %+v", last)
	}
	if n := countMethod(fj, "PUT"); n != 1 {
		t.Fatalf("PUT-запросов: %d", n)
	}
}

func TestAssignNoneYesPutsNullName(t *testing.T) {
	fj := newFakeJira(t, routes{
		"PUT /rest/api/2/issue/K-1/assignee": {204, ""},
	})
	out, err := fj.run("assign", "K-1", "none", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if out != "K-1: assignee → none\n" {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 1 || fj.requests[0].Body != `{"name":null}` {
		t.Fatalf("requests = %+v", fj.requests)
	}
}

func TestAssignUnknownUserFailsWithoutPut(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=nobody": {404, `{"errorMessages":["The user named 'nobody' does not exist"]}`},
	})
	_, err := fj.run("assign", "K-1", "nobody")
	if err == nil || err.Error() != "пользователь nobody не найден" {
		t.Fatalf("err = %v", err)
	}
	if n := countMethod(fj, "PUT"); n != 0 {
		t.Fatalf("PUT-запросов: %d", n)
	}
}

func TestAssignPreviewWithoutDisplayNameShowsUsernameOnly(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=bot": {200, `{"name":"bot","displayName":""}`},
		"GET /rest/api/2/issue/K-1":         {200, `{"key":"K-1","fields":{"summary":"S"}}`},
	})
	out, err := fj.run("assign", "K-1", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "K-1 S\nassignee: - → bot\n") {
		t.Fatalf("out = %q", out)
	}
}

// report --user: "" и "me" → myself без запроса /user; чужой username проверяется через /user,
// в отчёт и JQL идёт канонический name из ответа.
func TestReportUserResolution(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		user    string
		lookups int // запросов к /user
	}{
		{nil, "nizom", 0},
		{[]string{"--user", "me"}, "nizom", 0},
		{[]string{"--user", "Ivan"}, "ivan", 1}, // в отчёт и JQL — канонический name из Jira
	} {
		fj := newFakeJira(t, routes{
			"GET /rest/api/2/myself":             {200, `{"name":"nizom","displayName":"Низом Н."}`},
			"GET /rest/api/2/user?username=Ivan": {200, `{"name":"ivan","displayName":"Иван"}`},
			"GET /rest/api/2/search":             {200, `{"total":0,"issues":[]}`},
		})
		out, err := fj.run("report", append([]string{"2026-10"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if !strings.HasPrefix(out, "# 2026-10 · "+tc.user+" · ") {
			t.Errorf("%v: out = %q", tc.args, out)
		}
		lookups := 0
		for _, r := range fj.requests {
			if strings.HasPrefix(r.Path, "/rest/api/2/user") {
				lookups++
			}
			if strings.HasPrefix(r.Path, "/rest/api/2/search") && !strings.Contains(r.Path, "%22"+tc.user+"%22") {
				t.Errorf("%v: JQL без %q: %s", tc.args, tc.user, r.Path)
			}
		}
		if lookups != tc.lookups {
			t.Errorf("%v: запросов к /user: %d, want %d", tc.args, lookups, tc.lookups)
		}
	}
}

func TestReportUnknownUserFailsWithoutSearch(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=ivn": {404, `{"errorMessages":["The user named 'ivn' does not exist"]}`},
	})
	_, err := fj.run("report", "2026-10", "--user", "ivn")
	if err == nil || err.Error() != "пользователь ivn не найден" {
		t.Fatalf("err = %v", err)
	}
	if len(fj.requests) != 1 {
		t.Fatalf("requests = %+v", fj.requests)
	}
}

func TestLsUserResolution(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		who     string // assignee в JQL (url-кодированный)
		lookups int    // запросов пользователя: /user и /myself
	}{
		{nil, "currentUser%28%29", 0}, // свои задачи — без лишних запросов
		{[]string{"--user", "me"}, "currentUser%28%29", 0},
		{[]string{"--user", "Ivan"}, "%22ivan%22", 1}, // канонический name из Jira
	} {
		fj := newFakeJira(t, routes{
			"GET /rest/api/2/user?username=Ivan": {200, `{"name":"ivan","displayName":"Иван"}`},
			"GET /rest/api/2/search":             {200, `{"total":0,"issues":[]}`},
		})
		if _, err := fj.run("ls", tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		lookups := 0
		for _, r := range fj.requests {
			switch {
			case strings.HasPrefix(r.Path, "/rest/api/2/user"), strings.HasPrefix(r.Path, "/rest/api/2/myself"):
				lookups++
			case !strings.Contains(r.Path, "assignee+%3D+"+tc.who):
				t.Errorf("%v: JQL без assignee = %s: %s", tc.args, tc.who, r.Path)
			}
		}
		if lookups != tc.lookups {
			t.Errorf("%v: запросов пользователя: %d, want %d", tc.args, lookups, tc.lookups)
		}
	}
}

func TestLsUnknownUserFailsWithoutSearch(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=ivn": {404, `{"errorMessages":["The user named 'ivn' does not exist"]}`},
	})
	_, err := fj.run("ls", "--user", "ivn")
	if err == nil || err.Error() != "пользователь ivn не найден" {
		t.Fatalf("err = %v", err)
	}
	if len(fj.requests) != 1 {
		t.Fatalf("requests = %+v", fj.requests)
	}
}

func TestUserFilterRejectsNone(t *testing.T) {
	for _, cmd := range [][]string{{"ls", "--user", "none"}, {"report", "2026-10", "--user", "none"}} {
		fj := newFakeJira(t, routes{})
		_, err := fj.run(cmd[0], cmd[1:]...)
		if err == nil || err.Error() != "--user none: нет такого фильтра (без исполнителя — create --assignee none, assign KEY none)" {
			t.Fatalf("%v: err = %v", cmd, err)
		}
		if len(fj.requests) != 0 {
			t.Fatalf("%v: requests = %+v", cmd, fj.requests)
		}
	}
}

func TestReportBadMonthFailsBeforeUserLookup(t *testing.T) {
	fj := newFakeJira(t, routes{})
	if _, err := fj.run("report", "11.2026", "--user", "ivn"); err == nil {
		t.Fatal("ожидалась ошибка формата месяца")
	}
	if len(fj.requests) != 0 {
		t.Fatalf("requests = %+v", fj.requests)
	}
}
