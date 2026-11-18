// SPDX-License-Identifier: AGPL-3.0-only

// Package server збирає сервер MCP з груп інструментів (toolsets): режим лише
// читання, анотації, логування викликів і перетворення помилок на isError.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// Name — назва сервера в initialize.
const Name = "keycrm-mcp"

// instructions — підказка клієнту MCP про те, як користуватися інструментами.
const instructions = `Інструменти для keyCRM. Списки (list_*) повертають компактні проєкції й ознаку has_next; повні дані — через get_*. ` +
	`Ідентифікатори статусів, джерел, тегів, способів оплати беріть з інструментів групи reference. ` +
	`Інструменти з параметром confirm змінюють дані: спершу покажіть користувачу, що саме зміниться, і лише після його згоди викликайте з confirm: true.`

// Deps — залежності, які отримують toolsets під час реєстрації.
type Deps struct {
	// KeyCRM — клієнт API зі спільним лімітом запитів.
	KeyCRM *keycrm.Client
	// Upload — правила завантаження файлів.
	Upload keycrm.UploadConfig
	// Log — журнал у stderr; nil — нічого не пише.
	Log *slog.Logger
	// Version — версія сервера для initialize.
	Version string
}

// Toolset — група інструментів, яку вмикають через KEYCRM_TOOLSETS.
type Toolset struct {
	// Name — назва групи (orders, reference, …).
	Name string
	// Register додає інструменти групи через Add.
	Register func(r *Registry, d Deps)
}

// New створює сервер MCP з груп sets, увімкнених у cfg.Toolsets ("all" — усі).
// Невідома назва групи або некоректний опис інструмента — помилка.
func New(cfg config.Config, d Deps, sets []Toolset) (*mcp.Server, *Registry, error) {
	known := make([]string, 0, len(sets))
	for _, s := range sets {
		known = append(known, s.Name)
	}
	enabled := map[string]bool{}
	for _, name := range cfg.Toolsets {
		if name != "all" && !slices.Contains(known, name) {
			return nil, nil, fmt.Errorf("KEYCRM_TOOLSETS: unknown toolset %q (available: all, %s)", name, strings.Join(known, ", "))
		}
		enabled[name] = true
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	version := d.Version
	if version == "" {
		version = "dev"
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: Name, Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
		// Без можливості logging: логи йдуть у stderr, а не клієнту.
		Capabilities: &mcp.ServerCapabilities{},
	})
	r := &Registry{srv: srv, readOnly: cfg.ReadOnly, log: d.Log, names: map[string]bool{}}
	for _, s := range sets {
		if enabled["all"] || enabled[s.Name] {
			s.Register(r, d)
		}
	}
	if err := errors.Join(r.errs...); err != nil {
		return nil, nil, err
	}
	return srv, r, nil
}
