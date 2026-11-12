package main

import (
	"os"
	"strings"
	"testing"
)

func TestLoginRejectsUnknownFlagsBeforeReadingToken(t *testing.T) {
	devnull, err := os.Open(os.DevNull) // если флаг не проверен, login прочитает пустой токен, а не зависнет
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	prev := os.Stdin
	os.Stdin = devnull
	defer func() { os.Stdin = prev }()

	for _, args := range [][]string{{"--json"}, {"--yes"}} {
		fj := newFakeJira(t, routes{})
		out, err := fj.run("login", args...)
		if err == nil || !strings.Contains(err.Error(), "неизвестный флаг "+args[0]) {
			t.Errorf("login %v: err = %v", args, err)
		}
		if out != "" || len(fj.requests) != 0 {
			t.Errorf("login %v: out = %q, requests = %+v", args, out, fj.requests)
		}
	}
}

func TestMePrintsNameAndDisplayName(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/myself": {200, `{"name":"nizom","displayName":"Низом Н."}`},
	})
	out, err := fj.run("me")
	if err != nil {
		t.Fatal(err)
	}
	if out != "nizom\tНизом Н.\n" {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 1 {
		t.Fatalf("requests = %+v", fj.requests)
	}
	r := fj.requests[0]
	if r.Method != "GET" || r.Path != "/rest/api/2/myself" || r.Auth != "Bearer "+fakeToken {
		t.Fatalf("request = %+v", r)
	}
}
