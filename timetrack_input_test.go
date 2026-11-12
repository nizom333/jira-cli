package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ввод длительности (jira-cli-g8t.2): log/estimate/create --estimate против фейка Jira.

func worklogBody(t *testing.T, fj *fakeJira) map[string]any {
	t.Helper()
	if len(fj.requests) != 1 {
		t.Fatalf("ожидался один запрос, пришли: %+v", fj.requests)
	}
	r := fj.requests[0]
	if r.Method != "POST" || r.Path != "/rest/api/2/issue/K-1/worklog" {
		t.Fatalf("request = %+v", r)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatalf("тело %q: %v", r.Body, err)
	}
	return body
}

func TestLogSplitsDurationTokensFromComment(t *testing.T) {
	fj := newFakeJira(t, routes{"POST /rest/api/2/issue/K-1/worklog": {201, `{}`}})
	if _, err := fj.run("log", "K-1", "1d", "4h", "сделал", "фичу"); err != nil {
		t.Fatal(err)
	}
	body := worklogBody(t, fj)
	if body["timeSpent"] != "1d 4h" || body["comment"] != "сделал фичу" {
		t.Fatalf("body = %v", body)
	}
}

func TestLogNormalizesDuration(t *testing.T) {
	for _, tc := range []struct {
		args           []string
		spent, comment string
	}{
		{[]string{"2h30m"}, "2h 30m", ""},
		{[]string{"2H", "30M"}, "2h 30m", ""},
		{[]string{"1d4h", "коммент"}, "1d 4h", "коммент"},
		{[]string{"1d 4h", "что сделал"}, "1d 4h", "что сделал"},
		{[]string{"1d", "0h"}, "1d", ""},
		{[]string{"0d4H", "0m", "коммент"}, "4h", "коммент"},
	} {
		fj := newFakeJira(t, routes{"POST /rest/api/2/issue/K-1/worklog": {201, `{}`}})
		out, err := fj.run("log", append([]string{"K-1"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		body := worklogBody(t, fj)
		if body["timeSpent"] != tc.spent {
			t.Errorf("%v: timeSpent = %v, want %q", tc.args, body["timeSpent"], tc.spent)
		}
		if c, _ := body["comment"].(string); c != tc.comment {
			t.Errorf("%v: comment = %q, want %q", tc.args, c, tc.comment)
		}
		if !strings.HasPrefix(out, "K-1: списано "+tc.spent+" за ") {
			t.Errorf("%v: out = %q", tc.args, out)
		}
	}
}

func TestLogRejectsInvalidDurationWithoutRequests(t *testing.T) {
	for _, args := range [][]string{{"abc"}, {"2x"}, {}, {"0h"}, {"0d", "0h"}, {"2h30"}, {"сделал", "2h"}} {
		fj := newFakeJira(t, routes{})
		if _, err := fj.run("log", append([]string{"K-1"}, args...)...); err == nil {
			t.Errorf("log K-1 %v: ожидалась ошибка", args)
		}
		if len(fj.requests) != 0 {
			t.Errorf("log K-1 %v: запросы к Jira: %+v", args, fj.requests)
		}
	}
}

func TestLogDateSetsStartedAtNine(t *testing.T) {
	fj := newFakeJira(t, routes{"POST /rest/api/2/issue/K-1/worklog": {201, `{}`}})
	if _, err := fj.run("log", "K-1", "1h", "--date", "2026-11-10"); err != nil {
		t.Fatal(err)
	}
	if s, _ := worklogBody(t, fj)["started"].(string); !strings.HasPrefix(s, "2026-11-10T09:00:00.000") {
		t.Fatalf("started = %q", s)
	}
}

func TestEstimateNormalizesDuration(t *testing.T) {
	fj := newFakeJira(t, routes{"PUT /rest/api/2/issue/K-1": {204, ""}})
	out, err := fj.run("estimate", "K-1", "1d", "4h")
	if err != nil {
		t.Fatal(err)
	}
	if len(fj.requests) != 1 || fj.requests[0].Method != "PUT" || fj.requests[0].Path != "/rest/api/2/issue/K-1" {
		t.Fatalf("requests = %+v", fj.requests)
	}
	if want := `{"fields":{"timetracking":{"originalEstimate":"1d 4h"}}}`; strings.TrimSpace(fj.requests[0].Body) != want {
		t.Fatalf("body = %s", fj.requests[0].Body)
	}
	if out != "K-1: оценка 1d 4h\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestEstimateRejectsNonDurationWithoutRequests(t *testing.T) {
	for _, args := range [][]string{{"4h", "лишнее"}, {"abc"}, {}} {
		fj := newFakeJira(t, routes{})
		if _, err := fj.run("estimate", append([]string{"K-1"}, args...)...); err == nil {
			t.Errorf("estimate K-1 %v: ожидалась ошибка", args)
		}
		if len(fj.requests) != 0 {
			t.Errorf("estimate K-1 %v: запросы к Jira: %+v", args, fj.requests)
		}
	}
}

func TestCreatePreviewNormalizesEstimate(t *testing.T) {
	fj := newFakeJira(t, routes{})
	out, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--estimate", "1h30m")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "estimate: 1h 30m\n") {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 0 {
		t.Fatalf("превью ходило в Jira: %+v", fj.requests)
	}
}

func TestCreatePreviewRejectsInvalidEstimate(t *testing.T) {
	fj := newFakeJira(t, routes{})
	if _, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--estimate", "abc"); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if len(fj.requests) != 0 {
		t.Fatalf("запросы к Jira: %+v", fj.requests)
	}
}
