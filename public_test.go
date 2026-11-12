package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// privateWordsFile — список слов, которым нельзя попадать в публичный репозиторий (адрес своей Jira,
// ключи проектов): по одному регулярному выражению на строку, «#» — комментарий.
// Файла нет в git: сами слова тоже не публикуются. Нет файла — тест пропускается.
const privateWordsFile = ".private-words"

// В коде, документации и скилле нет привязки к конкретной компании.
// Своё (адрес Jira, статусы, шаблон задач) живёт в skill/local.md, которого нет в git.
func TestPublishedFilesHaveNoPrivateWords(t *testing.T) {
	list, err := os.ReadFile(privateWordsFile)
	if os.IsNotExist(err) {
		t.Skipf("нет %s — проверять нечего", privateWordsFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	var words []*regexp.Regexp
	for _, line := range strings.Split(string(list), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			words = append(words, regexp.MustCompile(line))
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "README.md", "GLOSSARY.md", "AGENTS.md", "Makefile", "skill/SKILL.md")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, w := range words {
				if w.MatchString(line) {
					t.Errorf("%s:%d: слово не для публикации (%s): %s", f, i+1, w, strings.TrimSpace(line))
				}
			}
		}
	}
}
