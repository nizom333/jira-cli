package main

// Состояние jira vi между запусками: вид (список или канбан), панель справа, проект и «мои или
// вся команда». Хранится в vi.json рядом с config.json и пишется при выходе из экрана, если что-то
// из этого выбрали в экране. Это не настройка: файла нет, он испорчен или относится к другой Jira —
// экран открывается как в первый раз.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	viStateList  = "list"
	viStateBoard = "board"
)

type viState struct {
	URL         string `json:"url"`            // Jira, к которой относятся проект и «вся команда»
	View        string `json:"view,omitempty"` // viStateList или viStateBoard
	HidePreview bool   `json:"hide_preview,omitempty"`
	Project     string `json:"project,omitempty"`
	Team        bool   `json:"team,omitempty"`
}

func viStatePath() (string, error) {
	p, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "vi.json"), nil
}

// loadViState — сохранённое состояние для Jira base. Проект и «вся команда» другой Jira
// не берутся; вид и панель — общие.
func loadViState(base string) viState {
	st := viState{URL: base}
	path, err := viStatePath()
	if err != nil {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var saved viState
	if json.Unmarshal(b, &saved) != nil {
		return st
	}
	st.View, st.HidePreview = saved.View, saved.HidePreview
	if saved.URL == base {
		st.Project, st.Team = saved.Project, saved.Team
	}
	return st
}

func saveViState(st viState) error {
	path, err := viStatePath()
	if err != nil {
		return err
	}
	return writeJSONFile(path, st)
}
