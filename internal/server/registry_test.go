// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

type pingIn struct {
	Mode  string   `json:"mode,omitempty" enum:"fast,slow" jsonschema:"режим"`
	Tags  []string `json:"tags,omitempty" enum:"a,b"`
	Phone string   `json:"phone,omitempty"`
}

type pingOut struct {
	OK bool `json:"ok"`
}

// testSet реєструє по одному інструменту кожного Kind і один, що повертає помилку.
var testSet = Toolset{Name: "test", Register: func(r *Registry, _ Deps) {
	ok := func(context.Context, pingIn) (pingOut, error) { return pingOut{OK: true}, nil }
	Add(r, ToolSpec{Name: "t_read", Description: "читання", Kind: KindRead, OperationIDs: []string{"op"}}, ok)
	Add(r, ToolSpec{Name: "t_write", Description: "запис", Kind: KindWrite, OperationIDs: []string{"op"}}, ok)
	Add(r, ToolSpec{Name: "t_idem", Description: "оновлення", Kind: KindIdempotent, OperationIDs: []string{"op"}}, ok)
	Add(r, ToolSpec{Name: "t_destr", Description: "видалення", Kind: KindDestructive, OperationIDs: []string{"op"}}, ok)
	Add(r, ToolSpec{Name: "t_fail", Description: "помилка", Kind: KindRead, OperationIDs: []string{"op"}},
		func(context.Context, pingIn) (pingOut, error) {
			return pingOut{}, &keycrm.Error{Status: 422, Message: "The given data was invalid.", Fields: map[string][]string{"sku": {"required"}}}
		})
}}

func toolsByName(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range cs.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		out[tool.Name] = tool
	}
	return out
}

func TestReadOnlySkipsWriteTools(t *testing.T) {
	cs := Connect(t, []Toolset{testSet}, Deps{}, true)
	got := toolsByName(t, cs)
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"t_fail", "t_read"}) {
		t.Fatalf("у read-only зареєстровано %v", names)
	}
}

func TestAnnotations(t *testing.T) {
	tools := toolsByName(t, Connect(t, []Toolset{testSet}, Deps{}, false))
	type hints struct{ ro, destr, idem bool }
	want := map[string]hints{
		"t_read":  {ro: true},
		"t_write": {},
		"t_idem":  {idem: true},
		"t_destr": {destr: true},
	}
	for name, w := range want {
		a := tools[name].Annotations
		if a == nil {
			t.Fatalf("%s: немає анотацій", name)
		}
		if a.ReadOnlyHint != w.ro || a.IdempotentHint != w.idem {
			t.Errorf("%s: readOnly=%v idempotent=%v", name, a.ReadOnlyHint, a.IdempotentHint)
		}
		if !w.ro {
			if a.DestructiveHint == nil || *a.DestructiveHint != w.destr {
				t.Errorf("%s: destructiveHint=%v, очікувано %v", name, a.DestructiveHint, w.destr)
			}
		}
	}
}

func TestEnumTagsInSchema(t *testing.T) {
	tools := toolsByName(t, Connect(t, []Toolset{testSet}, Deps{}, false))
	schema, ok := tools["t_read"].InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema: %T", tools["t_read"].InputSchema)
	}
	props := schema["properties"].(map[string]any)
	mode := props["mode"].(map[string]any)
	if !slices.Equal(toStrings(mode["enum"]), []string{"fast", "slow"}) || mode["description"] != "режим" {
		t.Errorf("mode: %v", mode)
	}
	items := props["tags"].(map[string]any)["items"].(map[string]any)
	if !slices.Equal(toStrings(items["enum"]), []string{"a", "b"}) {
		t.Errorf("tags.items: %v", items)
	}

	// Значення поза enum відхиляє сам SDK, до обробника.
	res := CallTool(t, Connect(t, []Toolset{testSet}, Deps{}, false), "t_read", map[string]any{"mode": "warp"})
	if !res.IsError {
		t.Error("очікувався IsError для значення поза enum")
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestErrorBecomesIsError(t *testing.T) {
	res := CallTool(t, Connect(t, []Toolset{testSet}, Deps{}, false), "t_fail", nil)
	if !res.IsError {
		t.Fatal("очікувався IsError")
	}
	if txt := ResultText(res); !strings.Contains(txt, "sku: required") || !strings.Contains(txt, "422") {
		t.Errorf("текст помилки: %q", txt)
	}
}

func TestCallIsLoggedRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cs := Connect(t, []Toolset{testSet}, Deps{Log: log}, false)
	CallTool(t, cs, "t_read", map[string]any{"phone": "+380671234567"})
	out := buf.String()
	if strings.Contains(out, "+380671234567") {
		t.Errorf("телефон потрапив у лог: %s", out)
	}
	if !strings.Contains(out, "t_read") || !strings.Contains(out, "+38067*****67") {
		t.Errorf("у лозі немає виклику: %s", out)
	}
}

func TestUnknownToolsetFails(t *testing.T) {
	_, _, err := New(config.Config{Toolsets: []string{"test", "nope"}}, Deps{}, []Toolset{testSet})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("очікувалась помилка про невідомий toolset, отримано %v", err)
	}
}

func TestToolsetsSelection(t *testing.T) {
	other := Toolset{Name: "other", Register: func(r *Registry, _ Deps) {
		Add(r, ToolSpec{Name: "o_read", Description: "x", Kind: KindRead, Local: true},
			func(context.Context, pingIn) (pingOut, error) { return pingOut{}, nil })
	}}
	_, reg, err := New(config.Config{Toolsets: []string{"other"}}, Deps{}, []Toolset{testSet, other})
	if err != nil {
		t.Fatal(err)
	}
	if specs := reg.Specs(); len(specs) != 1 || specs[0].Name != "o_read" {
		t.Errorf("Specs = %+v", specs)
	}
	_, reg, err = New(config.Config{Toolsets: []string{"all"}}, Deps{}, []Toolset{testSet, other})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reg.Specs()); n != 6 {
		t.Errorf("all: %d інструментів, очікувано 6", n)
	}
}

func TestDuplicateToolFails(t *testing.T) {
	dup := Toolset{Name: "dup", Register: func(r *Registry, _ Deps) {
		h := func(context.Context, pingIn) (pingOut, error) { return pingOut{}, nil }
		Add(r, ToolSpec{Name: "same", Description: "x", Kind: KindRead, Local: true}, h)
		Add(r, ToolSpec{Name: "same", Description: "x", Kind: KindRead, Local: true}, h)
	}}
	if _, _, err := New(config.Config{Toolsets: []string{"all"}}, Deps{}, []Toolset{dup}); err == nil {
		t.Fatal("очікувалась помилка для дубля назви")
	}
}

func TestRequireConfirm(t *testing.T) {
	if err := RequireConfirm(true, "create_order"); err != nil {
		t.Errorf("confirm=true: %v", err)
	}
	err := RequireConfirm(false, "create_order")
	if err == nil || !strings.Contains(err.Error(), "create_order") || !strings.Contains(err.Error(), "confirm: true") {
		t.Errorf("confirm=false: %v", err)
	}
}

func TestFormatError(t *testing.T) {
	plain := errors.New("boom")
	if got := FormatError(plain); got != "boom" {
		t.Errorf("FormatError(plain) = %q", got)
	}
	wrapped := errors.Join(errors.New("ctx"), &keycrm.Error{Status: 422, Message: "invalid", Fields: map[string][]string{"b": {"x"}, "a": {"y", "z"}}})
	got := FormatError(wrapped)
	if !strings.Contains(got, "a: y, z\n") || !strings.Contains(got, "b: x") || strings.Index(got, "a: ") > strings.Index(got, "b: ") {
		t.Errorf("FormatError(422) = %q", got)
	}
}
