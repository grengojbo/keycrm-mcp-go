// SPDX-License-Identifier: AGPL-3.0-only

// Package tools перелічує всі групи інструментів сервера.
package tools

import (
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/catalog"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/orders"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/reference"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/stock"
)

// All повертає всі групи інструментів у порядку реєстрації.
// Нова група додається сюди одним рядком.
func All() []server.Toolset {
	return []server.Toolset{
		reference.Toolset,
		orders.Toolset,
		catalog.Toolset,
		stock.Toolset,
	}
}
