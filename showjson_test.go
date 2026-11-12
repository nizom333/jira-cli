package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --json у команды без JSON отклоняется в run до connect: ни клиента, ни запросов, ни вывода.
func TestWriteCommandsRejectJSONBeforeConnect(t *testing.T) {
	for _, cmd := range []string{"login", "create", "assign", "move", "comment", "log", "estimate"} {
		var buf bytes.Buffer
		connected := false
		connect := func() (*Client, error) {
			connected = true
			return nil, errors.New("connect не должен вызываться")
		}
		err := run(&buf, cmd, []string{"K-1", "2h", "--json"}, connect)
		if err == nil || !strings.HasPrefix(err.Error(), "неизвестный флаг --json (см. jira help") {
			t.Errorf("%s: err = %v", cmd, err)
		}
		if connected || buf.Len() != 0 {
			t.Errorf("%s: connected = %v, out = %q", cmd, connected, buf.String())
		}
	}
}

func TestShowJSONParityFields(t *testing.T) {
	long := strings.Repeat("я", 4001)
	issue := `{"key":"K-2","fields":{"summary":"Дочерняя","description":"` + long + `",
		"status":{"name":"Выполнена"},"issuetype":{"name":"Задача"},
		"resolutiondate":"2026-11-05T12:00:00.000+0500",
		"parent":{"key":"K-1","fields":{"summary":"Родитель"}},
		"comment":{"total":0,"comments":[]}}}`
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-2": {200, issue}})
	out, err := fj.run("show", "K-2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("не JSON: %v\n%s", err, out)
	}
	if got["resolved"] != "2026-11-05" {
		t.Errorf("resolved = %v", got["resolved"])
	}
	if p, ok := got["parent"].(map[string]any); !ok || p["key"] != "K-1" || p["summary"] != "Родитель" {
		t.Errorf("parent = %v", got["parent"])
	}
	if got["description_truncated"] != true {
		t.Errorf("description_truncated = %v", got["description_truncated"])
	}
	if d, _ := got["description"].(string); len([]rune(d)) != 4000 {
		t.Errorf("длина description = %d", len([]rune(d)))
	}
	if !strings.Contains(out, `"comments":[]`) {
		t.Errorf("comments не пустой массив:\n%s", out)
	}
}

func TestWriteCommandsRejectJSONWithoutRequests(t *testing.T) {
	for _, args := range [][]string{
		{"comment", "K-1", "текст", "--json"},
		{"move", "K-1", "--json"},
		{"move", "K-1", "в работе", "--json"},
		{"log", "K-1", "2h", "--json"},
		{"estimate", "K-1", "4h", "--json"},
		{"assign", "K-1", "me", "--json"},
		{"create", "--project", "K", "--summary", "S", "--json"},
	} {
		fj := newFakeJira(t, routes{})
		out, err := fj.run(args[0], args[1:]...)
		if err == nil || !strings.Contains(err.Error(), "неизвестный флаг --json") {
			t.Errorf("%v: err = %v", args, err)
		}
		if out != "" || len(fj.requests) != 0 {
			t.Errorf("%v: out = %q, requests = %+v", args, out, fj.requests)
		}
	}
}

func TestShowJSONOpenIssueDefaults(t *testing.T) {
	issue := `{"key":"K-3","fields":{"summary":"S","description":"коротко","status":{"name":"Открыта"},
		"issuetype":{"name":"Задача"},"comment":{"total":0}}}`
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-3": {200, issue}})
	out, err := fj.run("show", "K-3", "--json", "--full")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"resolved":""`, `"description_truncated":false`, `"comments":[]`} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %s в JSON:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"parent"`) {
		t.Errorf("parent без родителя:\n%s", out)
	}
}
