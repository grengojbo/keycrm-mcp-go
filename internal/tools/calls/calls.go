// SPDX-License-Identifier: AGPL-3.0-only

// Package calls — журнал дзвінків keyCRM.
package calls

import (
	"context"
	"strings"

	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/buyers"
	"github.com/grengojbo/keycrm-mcp-go/internal/tools/listing"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm/api"
)

// Toolset — група calls.
var Toolset = server.Toolset{Name: "calls", Register: register}

// CallSummary — компактна проєкція дзвінка для list_calls.
type CallSummary struct {
	ID             int    `json:"id"`
	Type           string `json:"type,omitempty"`
	State          string `json:"state,omitempty"`
	ExternalNumber string `json:"external_number,omitempty"`
	InternalNumber string `json:"internal_number,omitempty"`
	Duration       int    `json:"duration"`
	ClientID       int    `json:"client_id,omitempty"`
	ClientName     string `json:"client_name,omitempty"`
	LeadID         int    `json:"lead_id,omitempty"`
	ManagerID      int    `json:"manager_id,omitempty"`
	ManagerName    string `json:"manager_name,omitempty"`
	Comment        string `json:"comment,omitempty"`
	CallURL        string `json:"call_url,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// rawCall — поля дзвінка keyCRM, з яких будується CallSummary.
type rawCall struct {
	ID             int    `json:"id"`
	Type           string `json:"type"`
	State          string `json:"state"`
	ExternalNumber string `json:"external_number"`
	InternalNumber string `json:"internal_number"`
	Duration       int    `json:"duration"`
	ClientID       int    `json:"client_id"`
	LeadID         int    `json:"lead_id"`
	UserID         int    `json:"user_id"`
	Comment        string `json:"comment"`
	CallURL        string `json:"call_url"`
	CreatedAt      string `json:"created_at"`
	Client         *struct {
		FullName string `json:"full_name"`
	} `json:"client"`
	Manager *struct {
		FullName string `json:"full_name"`
	} `json:"manager"`
}

func summarize(c rawCall) CallSummary {
	s := CallSummary{
		ID: c.ID, Type: c.Type, State: c.State, ExternalNumber: c.ExternalNumber, InternalNumber: c.InternalNumber,
		Duration: c.Duration, ClientID: c.ClientID, LeadID: c.LeadID, ManagerID: c.UserID,
		Comment: c.Comment, CallURL: c.CallURL, CreatedAt: c.CreatedAt,
	}
	if c.Client != nil {
		s.ClientName = c.Client.FullName
	}
	if c.Manager != nil {
		s.ManagerName = c.Manager.FullName
	}
	return s
}

// ListCallsInput — вхід list_calls.
type ListCallsInput struct {
	listing.Page
	Type           string `json:"type,omitempty" enum:"incoming,outgoing" jsonschema:"напрям: incoming — вхідні, outgoing — вихідні"`
	ClientID       []int  `json:"client_id,omitempty" jsonschema:"покупці (find_buyer, list_buyers)"`
	ClientPhone    string `json:"client_phone,omitempty" jsonschema:"телефон клієнта в будь-якому поширеному форматі; зводиться до +380671234567"`
	ManagerID      []int  `json:"manager_id,omitempty" jsonschema:"менеджери (list_users)"`
	LeadID         []int  `json:"lead_id,omitempty" jsonschema:"картки воронки"`
	ReferenceID    string `json:"reference_id,omitempty" jsonschema:"ідентифікатор дзвінка в телефонії"`
	CreatedBetween string `json:"created_between,omitempty" jsonschema:"період (UTC) через кому: 2026-11-01 00:00:00,2026-11-30 23:59:59"`
}

func register(r *server.Registry, d server.Deps) {

	server.Add(r, server.ToolSpec{
		Name: "list_calls",
		Description: "Журнал дзвінків keyCRM — компактно: напрям, стан, номер, тривалість (с), клієнт, менеджер, коментар, запис, дата. " +
			`Приклади: {"type": "incoming", "created_between": "2026-11-19 00:00:00,2026-11-19 23:59:59"}; {"client_id": [4545]}; ` +
			`{"client_phone": "067 123 45 67"}. Дати в UTC.`,
		Kind: server.KindRead, OperationIDs: []string{"getPaginatedListOfCalls"},
	}, func(ctx context.Context, in ListCallsInput) (listing.Result[CallSummary], error) {
		page, limit, note := in.Normalize()
		f := listing.Filter{}
		f.Set("type", in.Type)
		f.Set("client_id", in.ClientID)
		if strings.TrimSpace(in.ClientPhone) != "" {
			p, err := buyers.NormalizePhone(in.ClientPhone)
			if err != nil {
				return listing.Result[CallSummary]{}, err
			}
			f.Set("client_phone", p)
		}
		f.Set("manager_id", in.ManagerID)
		f.Set("lead_id", in.LeadID)
		f.Set("reference_id", in.ReferenceID)
		f.Set("created_between", in.CreatedBetween)
		params := &api.GetPaginatedListOfCallsParams{
			Page: &page, Limit: &limit, Filter: f.Param(),
			Include: listing.Include([]string{"client", "manager"}),
		}
		status, body, err := listing.Raw(d.KeyCRM.API.GetPaginatedListOfCalls(ctx, params))
		if err != nil {
			return listing.Result[CallSummary]{}, err
		}
		res, err := listing.Decode[rawCall](status, body, page, note)
		if err != nil {
			return listing.Result[CallSummary]{}, err
		}
		return listing.Project(res, summarize), nil
	})
}
