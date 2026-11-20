//go:build unix

package main

// Unix: raw-режим и размер окна — через stty (как и эхо при вводе токена, см. os_other.go),
// изменение размера — сигнал SIGWINCH.

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

type terminal struct {
	saved   string // состояние терминала до raw-режима (stty -g)
	winch   chan os.Signal
	resized chan struct{}
}

// isTerminal — stdin и stdout — терминал (не файл и не канал).
func isTerminal() bool { return isCharDevice(os.Stdin) && isCharDevice(os.Stdout) }

func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// stty выполняет stty над терминалом stdin.
func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// openTerminal переводит терминал в raw-режим: клавиши приходят сразу, без эха; Ctrl+C — клавиша, а не сигнал.
func openTerminal() (*terminal, error) {
	saved, err := stty("-g")
	if err != nil {
		return nil, fmt.Errorf("stty -g: %w", err)
	}
	if _, err := stty("raw", "-echo"); err != nil {
		stty(saved)
		return nil, fmt.Errorf("stty raw: %w", err)
	}
	t := &terminal{saved: saved, winch: make(chan os.Signal, 1), resized: make(chan struct{}, 1)}
	signal.Notify(t.winch, syscall.SIGWINCH)
	go func() {
		for range t.winch {
			select {
			case t.resized <- struct{}{}:
			default:
			}
		}
	}()
	return t, nil
}

func (t *terminal) resizes() <-chan struct{} { return t.resized }

// size — ширина и высота окна в символах.
func (t *terminal) size() (int, int, error) {
	out, err := stty("size")
	if err != nil {
		return 0, 0, err
	}
	var rows, cols int
	if _, err := fmt.Sscan(out, &rows, &cols); err != nil {
		return 0, 0, fmt.Errorf("stty size: %q", out)
	}
	return cols, rows, nil
}

func (t *terminal) restore() {
	signal.Stop(t.winch)
	close(t.winch)
	stty(t.saved)
}
