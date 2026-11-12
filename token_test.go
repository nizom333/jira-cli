package main

import (
	"io"
	"strings"
	"testing"
)

// Токен не должен попадать в argv (виден в ps) — только в stdin.
func TestSaveTokenCmdKeepsTokenOutOfArgv(t *testing.T) {
	const token = "NotARealToken0123456789+/="
	for goos, wantStdin := range map[string]string{
		"darwin": token + "\n" + token + "\n", // security просит ввести пароль дважды
		"linux":  token,
	} {
		cmd, err := saveTokenCmd(goos, "https://sd.example", token)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		for _, a := range cmd.Args {
			if strings.Contains(a, token) {
				t.Errorf("%s: токен в argv: %q", goos, cmd.Args)
			}
		}
		if cmd.Stdin == nil {
			t.Fatalf("%s: stdin не задан", goos)
		}
		got, _ := io.ReadAll(cmd.Stdin)
		if string(got) != wantStdin {
			t.Errorf("%s: stdin = %q, want %q", goos, got, wantStdin)
		}
	}
}

func TestSaveTokenCmdDarwinArgs(t *testing.T) {
	cmd, err := saveTokenCmd("darwin", "https://sd.example", "tok")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"security", "add-generic-password", "-U", "-s", "jira-cli", "-a", "sd.example", "-w"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
}

func TestSaveTokenCmdRejects(t *testing.T) {
	for name, tc := range map[string]struct{ goos, token string }{
		"длинный токен (security обрезает до 128)": {"darwin", strings.Repeat("A", 129)},
		"перевод строки в токене":                  {"darwin", "a\nb"},
		"неподдерживаемая ОС":                      {"plan9", "tok"},
	} {
		if _, err := saveTokenCmd(tc.goos, "https://sd.example", tc.token); err == nil {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
	if _, err := saveTokenCmd("darwin", "https://sd.example", strings.Repeat("A", 128)); err != nil {
		t.Errorf("128 символов должны проходить: %v", err)
	}
}
