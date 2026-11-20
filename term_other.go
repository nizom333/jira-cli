//go:build !unix && !windows

package main

import "errors"

var errNoTerminal = errors.New("интерактивный режим на этой ОС не поддерживается")

type terminal struct{}

func isTerminal() bool                       { return false }
func openTerminal() (*terminal, error)       { return nil, errNoTerminal }
func (t *terminal) resizes() <-chan struct{} { return nil }
func (t *terminal) size() (int, int, error)  { return 0, 0, errNoTerminal }
func (t *terminal) restore()                 {}
