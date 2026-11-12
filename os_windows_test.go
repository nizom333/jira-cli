//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Настоящий Диспетчер учётных данных: запись, чтение, перезапись и удаление токена
// тем же путём, каким идут login, команды и logout. Запись — под отдельным именем, после теста удаляется.
func TestWindowsCredentialManagerRoundTrip(t *testing.T) {
	t.Setenv("JIRA_TOKEN", "")
	base := fmt.Sprintf("https://jira-cli-test-%d.invalid", os.Getpid())
	t.Cleanup(func() { _ = deleteToken(base) })

	if _, err := loadToken(base); err == nil || !strings.Contains(err.Error(), "jira login") {
		t.Fatalf("до записи: err = %v, ожидалась подсказка про jira login", err)
	}
	if err := deleteToken(base); !errors.Is(err, errTokenNotFound) {
		t.Fatalf("удаление несуществующего: err = %v, want errTokenNotFound", err)
	}

	const first, second = "NotARealToken+/=first", "NotARealToken+/=second-длиннее"
	if err := saveToken(base, first); err != nil {
		t.Fatalf("запись: %v", err)
	}
	if got, err := loadToken(base); err != nil || got != first {
		t.Fatalf("чтение = %q, %v; want %q", got, err, first)
	}
	if err := saveToken(base, second); err != nil {
		t.Fatalf("перезапись: %v", err)
	}
	if got, err := loadToken(base); err != nil || got != second {
		t.Fatalf("после перезаписи = %q, %v; want %q", got, err, second)
	}
	// токен другого адреса не виден
	if _, err := loadToken(base + ".other"); err == nil {
		t.Error("токен виден под чужим адресом")
	}

	if err := deleteToken(base); err != nil {
		t.Fatalf("удаление: %v", err)
	}
	if _, err := loadToken(base); err == nil {
		t.Error("после удаления токен всё ещё читается")
	}
	if err := deleteToken(base); !errors.Is(err, errTokenNotFound) {
		t.Errorf("повторное удаление: err = %v, want errTokenNotFound", err)
	}
}

func TestWindowsTokenLimits(t *testing.T) {
	base := fmt.Sprintf("https://jira-cli-test-limits-%d.invalid", os.Getpid())
	t.Cleanup(func() { _ = deleteToken(base) })
	if err := saveToken(base, strings.Repeat("A", maxCredBlob+1)); err == nil {
		t.Error("токен длиннее предела: ожидалась ошибка")
	}
	if err := saveToken(base, "a\nb"); err == nil {
		t.Error("перевод строки в токене: ожидалась ошибка")
	}
	if err := saveToken(base, strings.Repeat("A", maxCredBlob)); err != nil {
		t.Errorf("токен ровно в предел: %v", err)
	}
}

// Без консоли (CI, перенаправленный ввод) переключение эха ничего не делает и не падает.
func TestWindowsEchoWithoutConsole(t *testing.T) {
	echo(false)
	echo(true)
}
