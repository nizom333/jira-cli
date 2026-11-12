package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// noControls валит тест, если в текстовом выводе остались управляющие символы, кроме \n и \t.
func noControls(t *testing.T, out string) {
	t.Helper()
	for _, r := range out {
		if (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("управляющий символ %U в выводе: %q", r, out)
		}
	}
}

const hostileIssue = `{"key":"K-1","fields":{
	"summary":"\u001b[31mКрасный\u001b[0m заголовок",
	"description":"строка1\r\nстрока2 \u001b]0;title\u0007конец\u009b2J",
	"status":{"name":"В работе\u001b[2K"},
	"issuetype":{"name":"Задача"},
	"assignee":{"name":"bob\u001b[8m","displayName":"Bob"},
	"comment":{"total":1,"comments":[{"author":{"name":"eve"},"created":"2026-11-10T10:00:00.000+0500","body":"привет\u007f\r\nмир\tок"}]}
}}`

func TestShowStripsControlChars(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-1": {200, hostileIssue}})
	out, err := fj.run("show", "K-1")
	if err != nil {
		t.Fatal(err)
	}
	noControls(t, out)
	for _, want := range []string{
		"K-1 [Задача] В работе[2K · -\n[31mКрасный[0m заголовок\n",
		"assignee: bob[8m",
		"\nстрока1\nстрока2 ]0;titleконец2J\n",
		"- eve 2026-11-10: привет\nмир\tок\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q в выводе:\n%s", want, out)
		}
	}
}

func TestLsStripsControlCharsAndKeepsOneLine(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/search": {200, `{"total":1,"issues":[{"key":"K-1","fields":{
		"summary":"\u001b[31mА\r\nБ\tВ\u0007","status":{"name":"Open\u009b"},"timetracking":{}}}]}`}})
	out, err := fj.run("ls")
	if err != nil {
		t.Fatal(err)
	}
	noControls(t, out)
	if want := "K-1\tOpen\t-\t-\t[31mА Б В\n"; out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestShowJSONKeepsControlCharsEscaped(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-1": {200, hostileIssue}})
	out, err := fj.run("show", "K-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	// Управляющие символы (C0 и C1) в JSON сохраняются, но экранированными.
	for _, want := range []string{`"summary":"\u001b[31mКрасный\u001b[0m заголовок"`, `строка1\r\nстрока2 \u001b]0;title\u0007конец\u009b2J`} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q в JSON:\n%s", want, out)
		}
	}
}

type stringResult struct {
	S string `json:"s"`
}

func (r stringResult) text(w io.Writer) { fmt.Fprintln(w, r.S) }

// render: текст чистится через textWriter, JSON пишется в сырой writer
// (управляющие символы — экранированными).
func TestRenderSanitizesTextEscapesJSON(t *testing.T) {
	var buf bytes.Buffer
	hostile := stringResult{"a\x1b[2Kb\u009bc"}
	if err := render(&buf, hostile, false); err != nil {
		t.Fatal(err)
	}
	if err := render(&buf, hostile, true); err != nil {
		t.Fatal(err)
	}
	if want := "a[2Kbc\n" + `{"s":"a\u001b[2Kb\u009bc"}` + "\n"; buf.String() != want {
		t.Fatalf("out = %q, want %q", buf.String(), want)
	}
}

// Однострочные поля с настоящим \n не должны порождать поддельные строки вывода.
const multilineIssue = `{"key":"K-1","fields":{
	"summary":"заголовок\nassignee: root","status":{"name":"В\nработе"},"issuetype":{"name":"За\nдача"},
	"priority":{"name":"High\n"},"assignee":{"name":"bob\nx"},"reporter":{"name":"eve\r\ny"},
	"parent":{"key":"K-0","fields":{"summary":"родитель\nparent: K-9"}},
	"comment":{"total":1,"comments":[{"author":{"name":"ann\nz"},"created":"2026-11-10T10:00:00.000+0500","body":"многострочный\nкомментарий"}]}
}}`

func TestShowKeepsSingleLineFieldsOnOneLine(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-1": {200, multilineIssue}})
	out, err := fj.run("show", "K-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "K-1 [За дача] В работе · High\nзаголовок assignee: root\n" +
		"assignee: bob x  reporter: eve y\n" +
		"estimate: -  spent: -  remaining: -\n" +
		"created: -  updated: -\n" +
		"parent: K-0 родитель parent: K-9\n" +
		"\nкомментарии (1 из 1):\n- ann z 2026-11-10: многострочный\nкомментарий\n"
	if out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
}

func TestAssignPreviewKeepsSingleLineFieldsOnOneLine(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/user?username=bob": {200, `{"name":"bob","displayName":"Bob\nassignee: x"}`},
		"GET /rest/api/2/issue/K-1":         {200, `{"key":"K-1","fields":{"summary":"S\nassignee: a → b","assignee":{"name":"eve\nq"}}}`},
	})
	out, err := fj.run("assign", "K-1", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "\nK-1 S assignee: a → b\nassignee: eve q → bob (Bob assignee: x)\n") {
		t.Fatalf("out = %q", out)
	}
}

// encoding/json оставляет DEL и C1 сырыми; render экранирует их, JSON декодируется в те же строки.
func TestShowJSONEscapesDELAndC1(t *testing.T) {
	const summary = "a\u007fb\u0080c\u009bd\u009fe"
	fj := newFakeJira(t, routes{"GET /rest/api/2/issue/K-1": {200,
		`{"key":"K-1","fields":{"summary":"a\u007fb\u0080c\u009bd\u009fe"}}`}})
	out, err := fj.run("show", "K-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if r >= 0x7f && r <= 0x9f {
			t.Fatalf("сырой %U в JSON: %q", r, out)
		}
	}
	if !strings.Contains(out, `"summary":"a\u007fb\u0080c\u009bd\u009fe"`) {
		t.Errorf("нет экранированного summary в %s", out)
	}
	var got struct{ Summary string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary != summary {
		t.Fatalf("summary = %q, want %q", got.Summary, summary)
	}
}

func TestStripControls(t *testing.T) {
	for in, want := range map[string]string{
		"обычный текст":             "обычный текст",
		"a\nb\tc":                   "a\nb\tc",
		"\x1b[31mred\x1b[0m":        "[31mred[0m",
		"\x1b]0;title\x07x":         "]0;titlex",
		"cr\r\nlf":                  "cr\nlf",
		"del\x7f c1\u009b c1\u0085": "del c1 c1",
		"bad\xffbyte":               "badbyte",
	} {
		if got := stripControls(in); got != want {
			t.Errorf("stripControls(%q) = %q, want %q", in, got, want)
		}
	}
}
