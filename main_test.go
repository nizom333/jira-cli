package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// usage перечисляет команды с --json вручную — список должен совпадать с таблицей commands.
func TestUsageListsJSONCommandsFromTable(t *testing.T) {
	_, rest, ok := strings.Cut(usage, "--json   сжатый JSON вместо текста — только ")
	if !ok {
		t.Fatal("в usage нет списка команд с --json")
	}
	listed, _, _ := strings.Cut(rest, "\n")
	got := strings.Split(listed, ", ")
	sort.Strings(got)
	var want []string
	for name, cmd := range commands {
		if cmd.json {
			want = append(want, name)
		}
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage: --json у %v, в таблице commands — у %v", got, want)
	}
}

func TestParseArgs(t *testing.T) {
	spec := flags("date=", "n=", "desc=", "yes")
	a, err := parseArgs([]string{"PROJ-1", "2h", "--date", "2026-11-10", "сделал", "фичу", "--yes", "-n=5", "--", "--не-флаг"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"PROJ-1", "2h", "сделал", "фичу", "--не-флаг"}; !reflect.DeepEqual(a.pos, want) {
		t.Fatalf("pos = %v", a.pos)
	}
	if a.get("date") != "2026-11-10" || !a.has("yes") || a.get("n") != "5" {
		t.Fatalf("flags = %v", a.flags)
	}
	if p, _ := parseArgs([]string{"--desc", "-"}, spec); p.get("desc") != "-" {
		t.Fatalf("stdin-маркер: %v", p.flags)
	}
}

// duration: в тексте — часы и минуты ("-" для пустого), в JSON — часы числом (0 для пустого).
func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		sec        int
		text, json string
	}{
		{0, "-", "0"}, {-60, "-", "0"}, {1800, "30m", "0.5"}, {1200, "20m", "0.33"},
		{3600, "1h", "1"}, {9000, "2h30m", "2.5"}, {57600, "16h", "16"},
	} {
		d := duration(tc.sec)
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if d.String() != tc.text || string(b) != tc.json {
			t.Errorf("duration(%d): text %q json %s, want %q %s", tc.sec, d.String(), b, tc.text, tc.json)
		}
	}
}

func TestMatchTransition(t *testing.T) {
	mk := func(id, name, to string) transition {
		tr := transition{ID: id, Name: name}
		tr.To.Name = to
		return tr
	}
	ts := []transition{
		mk("1", "Нужно сделать", "Открыта"),
		mk("2", "В работе", "В работе"),
		mk("3", "Выполнено", "Выполнена"),
		mk("4", "Приостановлено", "Приостановлено"),
	}
	if m := matchTransition(ts, "в работе"); len(m) != 1 || m[0].ID != "2" {
		t.Fatalf("exact: %v", m)
	}
	if m := matchTransition(ts, "выполнен"); len(m) != 1 || m[0].ID != "3" {
		t.Fatalf("substring: %v", m)
	}
	if m := matchTransition(ts, "о"); len(m) < 2 {
		t.Fatalf("ожидалась неоднозначность: %v", m)
	}
	if m := matchTransition(ts, "закрыть"); len(m) != 0 {
		t.Fatalf("ожидалось пусто: %v", m)
	}
}

func TestMonthRange(t *testing.T) {
	loc := time.FixedZone("+05", 5*3600)
	// 2026-11-30 21:00 UTC — уже 1 декабря по +05: текущий месяц берётся по поясу loc.
	now := time.Date(2026, 11, 30, 21, 0, 0, 0, time.UTC)
	w, _ := reportWindowFor("", now, loc)
	if w.start != time.Date(2026, 12, 1, 0, 0, 0, 0, loc) || w.end != time.Date(2027, 1, 1, 0, 0, 0, 0, loc) {
		t.Fatalf("%v %v", w.start, w.end)
	}
	w, _ = reportWindowFor("2026-10", now, loc)
	if w.start != time.Date(2026, 10, 1, 0, 0, 0, 0, loc) || w.end != time.Date(2026, 11, 1, 0, 0, 0, 0, loc) {
		t.Fatalf("%v %v", w.start, w.end)
	}
	if _, err := reportWindowFor("11.2026", now, loc); err == nil {
		t.Fatal("ожидалась ошибка формата")
	}
}

// Ошибка в stderr — одна строка без управляющих символов: текст сервера не подделает строку вывода.
func TestErrorLine(t *testing.T) {
	err := &APIError{Status: 400, ErrorMessages: []string{"плохо\nerror: поддельная\tстрока\x1b[31m"}}
	want := "error: HTTP 400: плохо error: поддельная строка[31m"
	if got := errorLine(err); got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}
