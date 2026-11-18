// SPDX-License-Identifier: AGPL-3.0-only

// Команда genclient генерує pkg/keycrm/api/api.gen.go з вендореного api.yaml.
//
// Специфікація keyCRM у кожній операції оголошує `security: - bearerAuth: {}`.
// Вимога безпеки — це масив скоупів, а не об'єкт, тож розбирач падає ще до того,
// як oapi-codegen застосує overlay.yaml. Тому genclient виправляє лише цей рядок
// у тимчасовій копії, а решту правок робить overlay. Сам api.yaml лишається
// незмінною копією https://docs.keycrm.app/api.yaml — оновлення специфікації
// зводиться до заміни файлу й `go generate ./...`.
//
// Запускається з каталогу pkg/keycrm через `go generate`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// emptySecurity знаходить вимогу безпеки, записану як порожній об'єкт.
var emptySecurity = regexp.MustCompile(`(?m)^(\s*-\s*bearerAuth:\s*)\{\}\s*$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genclient:", err)
		os.Exit(1)
	}
}

func run() error {
	spec, err := os.ReadFile("api.yaml")
	if err != nil {
		return fmt.Errorf("читання специфікації: %w", err)
	}
	fixed := emptySecurity.ReplaceAll(spec, []byte("${1}[]"))

	dir, err := os.MkdirTemp("", "keycrm-spec-")
	if err != nil {
		return fmt.Errorf("тимчасовий каталог: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tmp := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(tmp, fixed, 0o600); err != nil { //nolint:gosec // tmp — файл у власному каталозі з os.MkdirTemp
		return fmt.Errorf("запис тимчасової специфікації: %w", err)
	}

	cmd := exec.Command("go", "tool", "oapi-codegen", "-config", "oapi-codegen.yaml", tmp) //nolint:gosec // аргументи сталі, крім шляху до власного тимчасового файлу
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("oapi-codegen: %w", err)
	}
	return nil
}
