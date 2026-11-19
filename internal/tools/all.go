// SPDX-License-Identifier: AGPL-3.0-only

// Package tools перелічує всі групи інструментів сервера.
package tools

import (
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/buyers"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/calls"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/catalog"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/companies"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/files"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/orders"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/payments"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/pipelines"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/receipts"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/reference"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/stock"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/tasks"
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
		catalog.Toolset,
		stock.Toolset,
		receipts.Toolset,
		payments.Toolset,
		files.Toolset,
		tasks.Toolset,
		pipelines.Toolset,
	}
}
