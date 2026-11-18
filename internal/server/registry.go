// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/grengojbo/keycrm-mcp-go/internal/redact"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// Kind — вплив інструмента на дані keyCRM; з нього виводяться анотації MCP
// і рішення, чи реєструвати інструмент у режимі лише читання.
type Kind int

const (
	// KindRead — лише читає (GET).
	KindRead Kind = iota
	// KindWrite — створює нові дані (POST).
	KindWrite
	// KindIdempotent — оновлює; повторний виклик з тими самими аргументами нічого не змінює (PUT).
	KindIdempotent
	// KindDestructive — видаляє або від'єднує (DELETE, detach/remove).
	KindDestructive
)

// String повертає назву для логів.
func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	case KindIdempotent:
		return "idempotent"
	case KindDestructive:
		return "destructive"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// ToolSpec описує інструмент MCP.
type ToolSpec struct {
	// Name — назва інструмента MCP (англійською, snake_case).
	Name string
	// Description — опис для моделі українською, з прикладами.
	Description string
	// Kind — вплив на дані.
	Kind Kind
	// OperationIDs — операції api.yaml, які викликає інструмент; контрактний
	// тест перевіряє, що всі вони є в специфікації.
	OperationIDs []string
	// Local — інструмент не звертається до API keyCRM (OperationIDs порожній).
	Local bool
}

// Registry реєструє інструменти на сервері MCP з урахуванням режиму лише
// читання. Помилки опису інструментів накопичуються й повертаються з New.
type Registry struct {
	srv      *mcp.Server
	readOnly bool
	log      *slog.Logger
	specs    []ToolSpec
	names    map[string]bool
	errs     []error
}

// Specs повертає описи зареєстрованих інструментів у порядку реєстрації.
func (r *Registry) Specs() []ToolSpec {
	return slices.Clone(r.specs)
}

func (r *Registry) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

// Add реєструє інструмент з типізованим входом In і виходом Out.
//
// Схема входу виводиться з In: опис поля — тег `jsonschema:"…"`, допустимі
// значення — тег `enum:"a,b,c"` (для зрізу — значення елементів). Поля без
// omitempty обов'язкові. У режимі лише читання інструменти, крім KindRead,
// не реєструються. Кожен виклик логується з замаскованими аргументами;
// помилка обробника повертається клієнту як результат з isError: true і
// текстом FormatError.
func Add[In, Out any](r *Registry, spec ToolSpec, h func(ctx context.Context, in In) (Out, error)) {
	switch {
	case spec.Name == "" || spec.Description == "":
		r.fail("tool %q: name and description are required", spec.Name)
		return
	case r.names[spec.Name]:
		r.fail("tool %q: registered twice", spec.Name)
		return
	case !spec.Local && len(spec.OperationIDs) == 0:
		r.fail("tool %q: OperationIDs are required unless Local is set", spec.Name)
		return
	}
	r.names[spec.Name] = true
	if r.readOnly && spec.Kind != KindRead {
		return
	}
	schema, err := inputSchema[In]()
	if err != nil {
		r.fail("tool %q: input schema: %w", spec.Name, err)
		return
	}
	tool := &mcp.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: schema,
		Annotations: annotations(spec.Kind),
	}
	handler := func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		out, err := h(ctx, in)
		attrs := []any{"tool", spec.Name, "kind", spec.Kind.String(), "args", redact.Value(in), "duration", time.Since(start)}
		if err != nil {
			r.log.Warn("tool call failed", append(attrs, errorAttrs(err)...)...)
			var zero Out
			return nil, zero, toolError{err}
		}
		r.log.Info("tool call", attrs...)
		return nil, out, nil
	}
	if err := addTool(r.srv, tool, handler); err != nil {
		r.fail("tool %q: %w", spec.Name, err)
		return
	}
	r.specs = append(r.specs, spec)
}

// addTool викликає mcp.AddTool, який панікує на невалідній схемі виходу,
// і перетворює паніку на помилку старту.
func addTool[In, Out any](srv *mcp.Server, tool *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	mcp.AddTool(srv, tool, h)
	return nil
}

// annotations виводить підказки MCP з Kind. DestructiveHint за специфікацією
// за замовчуванням true, тому для інструментів запису він задається явно.
func annotations(k Kind) *mcp.ToolAnnotations {
	if k == KindRead {
		return &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	destructive := k == KindDestructive
	return &mcp.ToolAnnotations{
		DestructiveHint: &destructive,
		IdempotentHint:  k == KindIdempotent,
	}
}

// errorAttrs — атрибути логу для помилки без її тексту: текст може містити
// URL з фільтрами (телефон, email покупця).
func errorAttrs(err error) []any {
	var kerr *keycrm.Error
	if errors.As(err, &kerr) {
		return []any{"status", "error", "http_status", kerr.Status}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return []any{"status", "canceled"}
	}
	return []any{"status", "error", "error_type", fmt.Sprintf("%T", err)}
}

// toolError — помилка обробника з текстом для моделі.
type toolError struct{ err error }

func (e toolError) Error() string { return FormatError(e.err) }
func (e toolError) Unwrap() error { return e.err }

// FormatError повертає зрозумілий текст помилки. Для *keycrm.Error додає
// статус і помилки валідації по рядку на поле: "field: message".
func FormatError(err error) string {
	var kerr *keycrm.Error
	if !errors.As(err, &kerr) {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "keyCRM returned %d: %s", kerr.Status, kerr.Message)
	keys := make([]string, 0, len(kerr.Fields))
	for k := range kerr.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n%s: %s", k, strings.Join(kerr.Fields[k], ", "))
	}
	return b.String()
}

// RequireConfirm повертає помилку, якщо інструмент, що змінює дані, викликано
// без confirm: true. Модель має спершу показати користувачу, що зміниться.
func RequireConfirm(confirm bool, tool string) error {
	if confirm {
		return nil
	}
	return fmt.Errorf("%s changes data in keyCRM: call again with confirm: true after showing the user what will change", tool)
}

// inputSchema виводить JSON Schema з In і додає enum з тегів `enum:"…"`.
func inputSchema[In any]() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		return nil, err
	}
	applyEnums(reflect.TypeFor[In](), s)
	return s, nil
}

// applyEnums проходить поля структури t і ставить Enum відповідним
// властивостям схеми s (для зрізів — елементам), рекурсивно.
func applyEnums(t reflect.Type, s *jsonschema.Schema) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || s == nil {
		return
	}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" {
			applyEnums(f.Type, s) // вбудована структура: поля на тому ж рівні
			continue
		}
		if name == "" {
			name = f.Name
		}
		prop := s.Properties[name]
		if prop == nil {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if tag, ok := f.Tag.Lookup("enum"); ok {
			target := prop
			if ft.Kind() == reflect.Slice && prop.Items != nil {
				target = prop.Items
			}
			for v := range strings.SplitSeq(tag, ",") {
				target.Enum = append(target.Enum, v)
			}
		}
		switch ft.Kind() {
		case reflect.Struct:
			applyEnums(ft, prop)
		case reflect.Slice:
			applyEnums(ft.Elem(), prop.Items)
		}
	}
}
