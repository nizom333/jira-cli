package main

// Терминал для интерактивной команды jira vi: альтернативный экран, разбор клавиш, ширина текста
// и построчная отрисовка кадра. Только стандартная библиотека: raw-режим, размер окна и сигнал
// об изменении размера — в term_unix.go и term_windows.go.
//
// Программа — модель в стиле Elm (program): update(msg) меняет состояние и возвращает cmd —
// медленную работу (запрос к Jira), которую цикл выполняет в фоне и отдаёт её результат следующим
// сообщением; view(w, h) рисует кадр по состоянию. Модель не знает о терминале, поэтому тесты
// шлют ей клавиши и выполняют cmd сами.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

type msg any

// cmd — фоновая работа; её результат приходит в update следующим сообщением.
type cmd func() msg

type program interface {
	update(m msg) cmd
	view(w, h int) frame
	quitting() bool
}

// frame — кадр: h строк, каждая ровно шириной окна (стили — ANSI внутри строки);
// cursor — где показать курсор (поле ввода), nil — курсор скрыт.
type frame struct {
	lines  []string
	cursor *cursorPos
}

type cursorPos struct{ row, col int }

// resizeMsg — размер окна: первым сообщением и при каждом изменении.
type resizeMsg struct{ w, h int }

const (
	enterScreen = "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[2J" // альтернативный экран, без курсора и автопереноса
	leaveScreen = "\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l"
)

// runProgram крутит программу в терминале до выхода: клавиши, изменение размера окна и результаты
// cmd приходят в update по одному. Терминал восстанавливается при любом выходе, в том числе по панике.
func runProgram(p program) error {
	t, err := openTerminal()
	if err != nil {
		return err
	}
	defer t.restore()
	out := os.Stdout
	io.WriteString(out, enterScreen)
	defer io.WriteString(out, leaveScreen)

	w, h := windowSize(t)
	keys := make(chan key, 64)
	go readKeys(os.Stdin, keys)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)

	results := make(chan msg, 8)
	start := func(c cmd) {
		if c != nil {
			go func() { results <- c() }()
		}
	}
	scr := &screen{out: out}
	start(p.update(resizeMsg{w, h}))
	for !p.quitting() {
		scr.draw(p.view(w, h))
		var m msg
		select {
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			m = k
		case <-t.resizes():
			w, h = windowSize(t)
			scr.reset()
			m = resizeMsg{w, h}
		case m = <-results:
		case <-sig:
			return nil
		}
		start(p.update(m))
	}
	return nil
}

// windowSize — размер окна; если терминал его не сообщил — 80×24.
func windowSize(t *terminal) (int, int) {
	if w, h, err := t.size(); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 80, 24
}

// screen рисует кадры: перерисовываются только изменившиеся строки.
type screen struct {
	out  io.Writer
	prev []string
}

// reset — после изменения размера окна: стереть всё и нарисовать следующий кадр целиком.
func (s *screen) reset() {
	s.prev = nil
	io.WriteString(s.out, "\x1b[2J")
}

func (s *screen) draw(f frame) {
	var b bytes.Buffer
	b.WriteString("\x1b[?25l")
	for i, l := range f.lines {
		if i < len(s.prev) && s.prev[i] == l {
			continue
		}
		// строку сначала стираем целиком: если терминал считает ширину символа иначе, чем runeWidth,
		// от прошлого кадра не останется хвоста
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K%s\x1b[0m", i+1, l)
	}
	if f.cursor != nil {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", f.cursor.row+1, f.cursor.col+1)
	}
	s.out.Write(b.Bytes())
	s.prev = f.lines
}

// ---------- клавиши ----------

// key — нажатая клавиша: name — специальная клавиша; у обычного символа name == keyRune и r — символ.
type key struct {
	name keyName
	r    rune
}

type keyName string

const (
	keyRune       keyName = ""
	keyUp         keyName = "up"
	keyDown       keyName = "down"
	keyLeft       keyName = "left"
	keyRight      keyName = "right"
	keyShiftLeft  keyName = "shift+left"
	keyShiftRight keyName = "shift+right"
	keyHome       keyName = "home"
	keyEnd        keyName = "end"
	keyPgUp       keyName = "pgup"
	keyPgDown     keyName = "pgdown"
	keyEnter      keyName = "enter"
	keyEsc        keyName = "esc"
	keyTab        keyName = "tab"
	keyShiftTab   keyName = "shift+tab"
	keyBackspace  keyName = "backspace"
	keyDelete     keyName = "delete"
	keyCtrlA      keyName = "ctrl+a"
	keyCtrlC      keyName = "ctrl+c"
	keyCtrlE      keyName = "ctrl+e"
	keyCtrlN      keyName = "ctrl+n"
	keyCtrlP      keyName = "ctrl+p"
	keyCtrlU      keyName = "ctrl+u"
	keyCtrlW      keyName = "ctrl+w"
)

// is — это символ r.
func (k key) is(r rune) bool { return k.name == keyRune && k.r == r }

var ctrlKeys = map[byte]keyName{
	0x01: keyCtrlA, 0x03: keyCtrlC, 0x05: keyCtrlE, 0x08: keyBackspace, 0x09: keyTab,
	0x0a: keyEnter, 0x0d: keyEnter, 0x0e: keyCtrlN, 0x10: keyCtrlP, 0x15: keyCtrlU, 0x17: keyCtrlW,
	0x7f: keyBackspace,
}

// readKeys читает stdin и отправляет клавиши в out; конец ввода закрывает out.
func readKeys(r io.Reader, out chan<- key) {
	defer close(out)
	buf := make([]byte, 1024)
	var pending []byte // начало многобайтового символа, разрезанного между чтениями
	for {
		n, err := r.Read(buf)
		if n > 0 {
			var keys []key
			keys, pending = decodeKeys(append(pending, buf[:n]...))
			pending = append([]byte(nil), pending...)
			for _, k := range keys {
				out <- k
			}
		}
		if err != nil {
			return
		}
	}
}

// decodeKeys разбирает байты из терминала в клавиши; rest — незаконченный UTF-8 символ в конце.
// Неизвестные последовательности и управляющие байты отбрасываются.
func decodeKeys(b []byte) (keys []key, rest []byte) {
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			k, n := decodeEscape(b)
			if k != (key{}) {
				keys = append(keys, k)
			}
			b = b[n:]
		case c < 0x20 || c == 0x7f:
			if name, ok := ctrlKeys[c]; ok {
				keys = append(keys, key{name: name})
			}
			b = b[1:]
		default:
			if !utf8.FullRune(b) {
				return keys, b
			}
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError || n > 1 {
				keys = append(keys, key{r: r})
			}
			b = b[n:]
		}
	}
	return keys, nil
}

// decodeEscape разбирает последовательность, начинающуюся с ESC; n — сколько байт она заняла.
// Одиночный ESC — клавиша Esc; ESC перед обычным символом (Alt+символ) — Esc, символ разберётся отдельно.
func decodeEscape(b []byte) (key, int) {
	if len(b) == 1 {
		return key{name: keyEsc}, 1
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && b[i] >= 0x20 && b[i] <= 0x3f { // параметры и промежуточные байты CSI
			i++
		}
		if i >= len(b) {
			return key{}, len(b)
		}
		if b[i] < 0x40 || b[i] > 0x7e { // не финальный байт — обрывок, разбираем дальше с него
			return key{}, i
		}
		return csiKey(string(b[2:i]), b[i]), i + 1
	case 'O':
		if len(b) < 3 {
			return key{}, len(b)
		}
		return csiKey("", b[2]), 3
	}
	return key{name: keyEsc}, 1
}

// csiKey — клавиша по параметрам и финальному байту CSI (или SS3): стрелки, Home/End, PgUp/PgDn, Delete.
func csiKey(params string, final byte) key {
	params, mod, _ := strings.Cut(params, ";")
	shift := mod == "2"
	switch final {
	case 'A':
		return key{name: keyUp}
	case 'B':
		return key{name: keyDown}
	case 'C':
		if shift {
			return key{name: keyShiftRight}
		}
		return key{name: keyRight}
	case 'D':
		if shift {
			return key{name: keyShiftLeft}
		}
		return key{name: keyLeft}
	case 'H':
		return key{name: keyHome}
	case 'F':
		return key{name: keyEnd}
	case 'Z':
		return key{name: keyShiftTab}
	case '~':
		switch params {
		case "1", "7":
			return key{name: keyHome}
		case "4", "8":
			return key{name: keyEnd}
		case "3":
			return key{name: keyDelete}
		case "5":
			return key{name: keyPgUp}
		case "6":
			return key{name: keyPgDown}
		}
	}
	return key{}
}

// jcuken — русская раскладка → латинская по месту клавиши: клавиши-команды работают
// без переключения раскладки («о» — то же, что j). «.» и «,» — клавиши «/» и «?» в русской раскладке.
var jcuken = map[rune]rune{
	'й': 'q', 'ц': 'w', 'у': 'e', 'к': 'r', 'е': 't', 'н': 'y', 'г': 'u', 'ш': 'i', 'щ': 'o', 'з': 'p',
	'ф': 'a', 'ы': 's', 'в': 'd', 'а': 'f', 'п': 'g', 'р': 'h', 'о': 'j', 'л': 'k', 'д': 'l',
	'я': 'z', 'ч': 'x', 'с': 'c', 'м': 'v', 'и': 'b', 'т': 'n', 'ь': 'm',
	'Й': 'Q', 'Ц': 'W', 'У': 'E', 'К': 'R', 'Е': 'T', 'Н': 'Y', 'Г': 'U', 'Ш': 'I', 'Щ': 'O', 'З': 'P',
	'Ф': 'A', 'Ы': 'S', 'В': 'D', 'А': 'F', 'П': 'G', 'Р': 'H', 'О': 'J', 'Л': 'K', 'Д': 'L',
	'Я': 'Z', 'Ч': 'X', 'С': 'C', 'М': 'V', 'И': 'B', 'Т': 'N', 'Ь': 'M',
	'.': '/', ',': '?',
}

// latinKey — клавиша-команда в латинской раскладке (для полей ввода не применяется).
func latinKey(k key) key {
	if k.name == keyRune {
		if r, ok := jcuken[k.r]; ok {
			k.r = r
		}
	}
	return k
}

// ---------- текст ----------

// wideRanges — символы двойной ширины (East Asian Wide/Fullwidth и эмодзи), приблизительно.
var wideRanges = [][2]rune{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2329, 0x232a}, {0x23e9, 0x23ec}, {0x23f0, 0x23f0},
	{0x23f3, 0x23f3}, {0x25fd, 0x25fe}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267f, 0x267f},
	{0x2693, 0x2693}, {0x26a1, 0x26a1}, {0x26aa, 0x26ab}, {0x26bd, 0x26be}, {0x26c4, 0x26c5},
	{0x26ce, 0x26ce}, {0x26d4, 0x26d4}, {0x26ea, 0x26ea}, {0x26f2, 0x26f3}, {0x26f5, 0x26f5},
	{0x26fa, 0x26fa}, {0x26fd, 0x26fd}, {0x2705, 0x2705}, {0x270a, 0x270b}, {0x2728, 0x2728},
	{0x274c, 0x274c}, {0x274e, 0x274e}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27b0, 0x27b0}, {0x27bf, 0x27bf}, {0x2b1b, 0x2b1c}, {0x2b50, 0x2b50}, {0x2b55, 0x2b55},
	{0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf}, {0x4e00, 0x9fff}, {0xa000, 0xa4cf},
	{0xa960, 0xa97f}, {0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe6f},
	{0xff00, 0xff60}, {0xffe0, 0xffe6}, {0x16fe0, 0x16fe4}, {0x17000, 0x18cff}, {0x1b000, 0x1b2ff},
	{0x1f004, 0x1f004}, {0x1f0cf, 0x1f0cf}, {0x1f18e, 0x1f18e}, {0x1f191, 0x1f19a}, {0x1f200, 0x1f251},
	{0x1f300, 0x1f64f}, {0x1f680, 0x1f6ff}, {0x1f7e0, 0x1f7eb}, {0x1f90c, 0x1f9ff}, {0x1fa70, 0x1faff},
	{0x20000, 0x2fffd}, {0x30000, 0x3fffd},
}

// runeWidth — сколько колонок терминала занимает символ: 0 (управляющие, комбинируемые), 1 или 2.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r < 0x300:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	}
	for _, rg := range wideRanges {
		if r < rg[0] {
			break
		}
		if r <= rg[1] {
			return 2
		}
	}
	return 1
}

func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// clean — строка для экрана: без управляющих символов (ESC-последовательности с сервера не дойдут
// до терминала), переводы строк и табуляции — пробелы. Пробелы не схлопываются: отступы сохраняются.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, stripControls(s))
}

// fit — s в одну строку ровно шириной w: длинное обрезается с «…», короткое дополняется пробелами.
func fit(s string, w int) string { return pad(truncate(clean(s), w), w) }

// fitRight — как fit, но короткое прижимается вправо.
func fitRight(s string, w int) string {
	s = truncate(clean(s), w)
	return strings.Repeat(" ", max(0, w-textWidth(s))) + s
}

// pad дополняет уже чистую строку пробелами до ширины w.
func pad(s string, w int) string {
	if n := w - textWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// truncate обрезает уже чистую строку до ширины w, ставя «…» в конце.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runeWidth(r)
		if cur+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	b.WriteString("…")
	return b.String()
}

// splitWidth делит строку на начало шириной не больше w (хотя бы один символ) и остаток.
func splitWidth(s string, w int) (string, string) {
	cur := 0
	for i, r := range s {
		rw := runeWidth(r)
		if cur+rw > w && i > 0 {
			return s[:i], s[i:]
		}
		cur += rw
	}
	return s, ""
}

// wrap переносит абзац по словам в строки шириной не больше w; слово длиннее строки режется.
func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curW = 0
	}
	for _, word := range strings.Fields(clean(s)) {
		for textWidth(word) > w {
			if curW > 0 {
				flush()
			}
			var head string
			head, word = splitWidth(word, w)
			lines = append(lines, head)
		}
		if word == "" {
			continue
		}
		ww := textWidth(word)
		if curW > 0 && curW+1+ww > w {
			flush()
		}
		if curW > 0 {
			cur.WriteByte(' ')
			curW++
		}
		cur.WriteString(word)
		curW += ww
	}
	if curW > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// ---------- стили ----------

// noColor — NO_COLOR (no-color.org): без цветов; выделение (инверсия), жирный и тусклый остаются.
var noColor = os.Getenv("NO_COLOR") != ""

const (
	styleBold    = "\x1b[1m"
	styleDim     = "\x1b[2m"
	styleReverse = "\x1b[7m"
)

// color — цвет переднего плана (код SGR) или "" при NO_COLOR.
func color(code string) string {
	if noColor {
		return ""
	}
	return "\x1b[" + code + "m"
}

// paint оборачивает уже подогнанный по ширине текст в стиль.
func paint(style, s string) string {
	if style == "" {
		return s
	}
	return style + s + "\x1b[0m"
}
