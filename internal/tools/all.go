// SPDX-License-Identifier: AGPL-3.0-only

// Package tools перелічує всі групи інструментів сервера.
package tools

import (
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/buyers"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/calls"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/companies"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/orders"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/reference"
)

// All повертає всі групи інструментів у порядку реєстрації.
// Нова група додається сюди одним рядком.
func All() []server.Toolset {
	return []server.Toolset{
		reference.Toolset,
		orders.Toolset,
		buyers.Toolset,
		companies.Toolset,
		calls.Toolset,
	}
}
