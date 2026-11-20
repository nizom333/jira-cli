package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeKeys(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []key
	}{
		{"\x1b[A\x1b[B\x1b[C\x1b[D", []key{{name: keyUp}, {name: keyDown}, {name: keyRight}, {name: keyLeft}}},
		{"\x1bOA\x1bOH\x1bOF", []key{{name: keyUp}, {name: keyHome}, {name: keyEnd}}},
		{"\x1b[1;2C\x1b[1;2D\x1b[1;5C", []key{{name: keyShiftRight}, {name: keyShiftLeft}, {name: keyRight}}},
		{"\x1b[5~\x1b[6~\x1b[3~\x1b[1~\x1b[4~\x1b[Z", []key{{name: keyPgUp}, {name: keyPgDown}, {name: keyDelete}, {name: keyHome}, {name: keyEnd}, {name: keyShiftTab}}},
		{"\x1b", []key{{name: keyEsc}}},
		{"\x1bj", []key{{name: keyEsc}, {r: 'j'}}}, // Alt+j
		{"\r\n\x7f\x08\t\x03\x15\x17", []key{{name: keyEnter}, {name: keyEnter}, {name: keyBackspace}, {name: keyBackspace}, {name: keyTab}, {name: keyCtrlC}, {name: keyCtrlU}, {name: keyCtrlW}}},
		{"jкG 😀", []key{{r: 'j'}, {r: 'к'}, {r: 'G'}, {r: ' '}, {r: '😀'}}},
		{"\x1b[200~x\x1b[201~\x1b[99X", []key{{r: 'x'}}}, // неизвестные последовательности отбрасываются
		{"\x1b[\x1b[A", []key{{name: keyUp}}},            // обрывок CSI
		{"\x00\x02\xff", nil},
	} {
		got, rest := decodeKeys([]byte(tc.in))
		if !reflect.DeepEqual(got, tc.want) || len(rest) != 0 {
			t.Errorf("%q: got %+v (rest %q), want %+v", tc.in, got, rest, tc.want)
		}
	}
	// символ, разрезанный между двумя чтениями
	got, rest := decodeKeys([]byte("a\xd0"))
	if !reflect.DeepEqual(got, []key{{r: 'a'}}) || string(rest) != "\xd0" {
		t.Fatalf("got %+v, rest %q", got, rest)
	}
	got, rest = decodeKeys(append(rest, 0xbe))
	if !reflect.DeepEqual(got, []key{{r: 'о'}}) || len(rest) != 0 {
		t.Fatalf("got %+v, rest %q", got, rest)
	}
}

func TestReadKeysAcrossReads(t *testing.T) {
	r := &chunkReader{chunks: []string{"j\xd0", "\xbe\x1b[A", "q"}}
	out := make(chan key, 16)
	readKeys(r, out)
	var got []key
	for k := range out {
		got = append(got, k)
	}
	if want := []key{{r: 'j'}, {r: 'о'}, {name: keyUp}, {r: 'q'}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

type chunkReader struct{ chunks []string }

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, bytes.ErrTooLarge // любой конец ввода
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

func TestLatinKey(t *testing.T) {
	for in, want := range map[rune]rune{'о': 'j', 'л': 'k', 'Р': 'H', 'Д': 'L', 'в': 'd', 'н': 'y', '.': '/', ',': '?', 'j': 'j', 'ж': 'ж'} {
		if got := latinKey(key{r: in}); got.r != want {
			t.Errorf("%c → %c, want %c", in, got.r, want)
		}
	}
	if got := latinKey(key{name: keyUp}); got.name != keyUp {
		t.Errorf("up → %+v", got)
	}
}

func TestTextWidthAndFit(t *testing.T) {
	for _, tc := range []struct {
		s     string
		width int
	}{
		{"Привет", 6}, {"日本語", 6}, {"a😀b", 4}, {"е́", 1}, {"\x1b[31m", 4}, {"", 0},
	} {
		if got := textWidth(tc.s); got != tc.width {
			t.Errorf("textWidth(%q) = %d, want %d", tc.s, got, tc.width)
		}
	}
	for _, tc := range []struct {
		s    string
		w    int
		want string
	}{
		{"Привет", 4, "При…"},
		{"Привет", 8, "Привет  "},
		{"日本語", 5, "日本…"},
		{"日本語", 4, "日… "}, // «…» после широкого символа: пробел добивает ширину
		{"a\nb\tc", 6, "a b c "},
		{"x\x1b[2Jy", 6, "x[2Jy "},
		{"abc", 0, ""},
	} {
		if got := fit(tc.s, tc.w); got != tc.want || textWidth(got) != tc.w {
			t.Errorf("fit(%q, %d) = %q, want %q", tc.s, tc.w, got, tc.want)
		}
	}
	if got := fitRight("2h", 5); got != "   2h" {
		t.Errorf("fitRight = %q", got)
	}
}

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		s    string
		w    int
		want []string
	}{
		{"один два три", 8, []string{"один два", "три"}},
		{"  один\nдва  ", 20, []string{"один два"}},
		{"", 5, []string{""}},
		{"оченьдлинноеслово x", 5, []string{"очень", "длинн", "оесло", "во x"}},
		{"日本語の", 3, []string{"日", "本", "語", "の"}},
	} {
		if got := wrap(tc.s, tc.w); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("wrap(%q, %d) = %q, want %q", tc.s, tc.w, got, tc.want)
		}
	}
}

func TestScreenRedrawsOnlyChangedLines(t *testing.T) {
	var out bytes.Buffer
	s := &screen{out: &out}
	s.draw(frame{lines: []string{"aaa", "bbb", "ccc"}})
	first := out.String()
	for _, want := range []string{"\x1b[1;1H\x1b[2Kaaa", "\x1b[2;1H\x1b[2Kbbb", "\x1b[3;1H\x1b[2Kccc"} {
		if !strings.Contains(first, want) {
			t.Fatalf("первый кадр без %q: %q", want, first)
		}
	}
	out.Reset()
	s.draw(frame{lines: []string{"aaa", "BBB", "ccc"}, cursor: &cursorPos{row: 1, col: 2}})
	if got := out.String(); got != "\x1b[?25l\x1b[2;1H\x1b[2KBBB\x1b[0m\x1b[2;3H\x1b[?25h" {
		t.Fatalf("второй кадр: %q", got)
	}
}
