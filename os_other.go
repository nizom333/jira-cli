//go:build !windows

package main

// Не Windows: токен хранят внешние программы (security, secret-tool — см. client.go),
// эхо терминала переключает stty.

import (
	"os"
	"os/exec"
)

func osLoadToken(string) (string, error) { return "", errNoTokenStore }
func osSaveToken(string, string) error   { return errNoTokenStore }
func osDeleteToken(string) error         { return errNoTokenStore }

// echo включает/выключает эхо терминала при вводе токена.
func echo(on bool) {
	mode := "-echo"
	if on {
		mode = "echo"
	}
	cmd := exec.Command("stty", mode)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}
