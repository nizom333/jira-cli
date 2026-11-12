package main

import (
	"io"
	"strings"
	"unicode/utf8"
)

// Строки с сервера Jira (summary, description, комментарии, имена) могут содержать
// ESC/CSI/OSC-последовательности, которые подменяют вывод терминала. Текстовый вывод
// всех команд проходит через textWriter (см. render), поэтому чистить каждый Printf не нужно.
// JSON-вывод не чистим, а экранируем: управляющие символы остаются в данных (см. render).

// isControl: C0 (кроме \n и \t), DEL и C1.
func isControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f)
}

// stripControls удаляет управляющие символы (кроме \n и \t) и байты невалидного UTF-8.
func stripControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return isControl(r) || r == utf8.RuneError }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if !isControl(r) && !(r == utf8.RuneError && n == 1) {
			b.WriteString(s[:n])
		}
		s = s[n:]
	}
	return b.String()
}

// textWriter пропускает запись через stripControls. fmt.Fprint* пишет каждый вызов
// одним Write, так что многобайтовые руны не разрезаются.
type textWriter struct{ w io.Writer }

func (t textWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(t.w, stripControls(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
