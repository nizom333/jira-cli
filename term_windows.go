//go:build windows

package main

// Windows: raw-режим консоли и VT-последовательности — через SetConsoleMode (Windows 10+),
// размер окна — GetConsoleScreenBufferInfo. SIGWINCH нет: размер окна опрашивается.

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")

const (
	conProcessedInput  = 0x0001
	conLineInput       = 0x0002
	conEchoInput       = 0x0004
	conVTInput         = 0x0200 // ENABLE_VIRTUAL_TERMINAL_INPUT: стрелки приходят ESC-последовательностями
	conProcessedOutput = 0x0001
	conVTProcessing    = 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING: консоль понимает ESC-последовательности
)

type terminal struct {
	in, out         syscall.Handle
	inMode, outMode uint32
	resized         chan struct{}
	stop            chan struct{}
}

// isTerminal — stdin и stdout — консоль (не файл и не канал).
func isTerminal() bool {
	var m uint32
	return syscall.GetConsoleMode(syscall.Handle(os.Stdin.Fd()), &m) == nil &&
		syscall.GetConsoleMode(syscall.Handle(os.Stdout.Fd()), &m) == nil
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	if r, _, e := procSetConsoleMode.Call(uintptr(h), uintptr(mode)); r == 0 {
		return e
	}
	return nil
}

func openTerminal() (*terminal, error) {
	t := &terminal{in: syscall.Handle(os.Stdin.Fd()), out: syscall.Handle(os.Stdout.Fd()),
		resized: make(chan struct{}, 1), stop: make(chan struct{})}
	if err := syscall.GetConsoleMode(t.in, &t.inMode); err != nil {
		return nil, err
	}
	if err := syscall.GetConsoleMode(t.out, &t.outMode); err != nil {
		return nil, err
	}
	if err := setConsoleMode(t.in, t.inMode&^(conEchoInput|conLineInput|conProcessedInput)|conVTInput); err != nil {
		return nil, err
	}
	if err := setConsoleMode(t.out, t.outMode|conProcessedOutput|conVTProcessing); err != nil {
		setConsoleMode(t.in, t.inMode)
		return nil, errors.New("консоль не понимает ESC-последовательности (нужен Windows 10 или новее)")
	}
	go t.watchSize()
	return t, nil
}

func (t *terminal) resizes() <-chan struct{} { return t.resized }

type consoleCoord struct{ x, y int16 }

type consoleRect struct{ left, top, right, bottom int16 }

// consoleScreenBufferInfo — CONSOLE_SCREEN_BUFFER_INFO из wincon.h.
type consoleScreenBufferInfo struct {
	size              consoleCoord
	cursorPosition    consoleCoord
	attributes        uint16
	window            consoleRect
	maximumWindowSize consoleCoord
}

// size — ширина и высота видимой части окна консоли.
func (t *terminal) size() (int, int, error) {
	var info consoleScreenBufferInfo
	if r, _, e := procGetConsoleScreenBufferInfo.Call(uintptr(t.out), uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, e
	}
	return int(info.window.right-info.window.left) + 1, int(info.window.bottom-info.window.top) + 1, nil
}

func (t *terminal) watchSize() {
	w, h, _ := t.size()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			nw, nh, err := t.size()
			if err != nil || (nw == w && nh == h) {
				continue
			}
			w, h = nw, nh
			select {
			case t.resized <- struct{}{}:
			default:
			}
		}
	}
}

func (t *terminal) restore() {
	close(t.stop)
	setConsoleMode(t.in, t.inMode)
	setConsoleMode(t.out, t.outMode)
}
