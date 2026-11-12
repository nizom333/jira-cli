//go:build windows

package main

// Windows: токен хранится в Диспетчере учётных данных (Credential Manager) как Generic Credential,
// эхо консоли выключается через SetConsoleMode. Только стандартная библиотека — вызовы WinAPI напрямую.

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	advapi32           = syscall.NewLazyDLL("advapi32.dll")
	procCredRead       = advapi32.NewProc("CredReadW")
	procCredWrite      = advapi32.NewProc("CredWriteW")
	procCredDelete     = advapi32.NewProc("CredDeleteW")
	procCredFree       = advapi32.NewProc("CredFree")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2    // переживает перезагрузку, виден только этому пользователю
	maxCredBlob             = 2560 // CRED_MAX_CREDENTIAL_BLOB_SIZE
	errorNotFound           = syscall.Errno(1168)
	enableEchoInput         = 0x4
)

// credential — структура CREDENTIALW из wincred.h.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func osLoadToken(target string) (string, error) {
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return "", err
	}
	var cred *credential
	r, _, e := procCredRead.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred)))
	if r == 0 {
		if e == errorNotFound {
			return "", errTokenNotFound
		}
		return "", e
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlob == nil || cred.CredentialBlobSize == 0 {
		return "", errTokenNotFound
	}
	return string(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), nil // string копирует до CredFree
}

func osSaveToken(target, token string) error {
	blob := []byte(token)
	if len(blob) == 0 || len(blob) > maxCredBlob {
		return fmt.Errorf("токен длиной %d байт не поместится в Диспетчер учётных данных: используйте JIRA_TOKEN", len(blob))
	}
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString(keychainService)
	if err != nil {
		return err
	}
	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	if r, _, e := procCredWrite.Call(uintptr(unsafe.Pointer(&cred)), 0); r == 0 {
		return e
	}
	return nil
}

func osDeleteToken(target string) error {
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if r, _, e := procCredDelete.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0); r == 0 {
		if e == errorNotFound {
			return errTokenNotFound
		}
		return e
	}
	return nil
}

// echo включает/выключает эхо консоли при вводе токена.
func echo(on bool) {
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return
	}
	if on {
		mode |= enableEchoInput
	} else {
		mode &^= enableEchoInput
	}
	procSetConsoleMode.Call(uintptr(h), uintptr(mode))
}
