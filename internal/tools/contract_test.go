// SPDX-License-Identifier: AGPL-3.0-only

package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
	"github.com/grengojbo/keycrm-mcp-go/pkg/keycrm"
)

// TestContractEveryToolMapsToSpec перевіряє, що кожен інструмент викликає лише
// операції з вендореного api.yaml (офіційний публічний API) і що назви унікальні.
func TestContractEveryToolMapsToSpec(t *testing.T) {
	ops, err := keycrm.SpecOperationIDs()
	if err != nil {
		t.Fatal(err)
	}
	_, reg, err := server.New(config.Config{Toolsets: []string{"all"}}, server.Deps{}, All())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	specs := reg.Specs()
	if len(specs) == 0 {
		t.Fatal("жодного інструмента")
	}
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.Name] {
			t.Errorf("%s: назва повторюється", s.Name)
		}
		seen[s.Name] = true
		if s.Local {
			continue
		}
		if len(s.OperationIDs) == 0 {
			t.Errorf("%s: немає OperationIDs", s.Name)
		}
		for _, op := range s.OperationIDs {
			if _, ok := ops[op]; !ok {
				t.Errorf("%s: operationId %q немає в api.yaml", s.Name, op)
			}
		}
	}
}

// TestToolsetNamesUnique перевіряє, що назви груп унікальні й не "all".
func TestToolsetNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range All() {
		if s.Name == "" || s.Name == "all" || seen[s.Name] {
			t.Errorf("некоректна або повторна назва групи %q", s.Name)
		}
		seen[s.Name] = true
	}
}

// TestReadOnlyRegistersOnlyReadTools — у режимі лише читання жодного інструмента запису.
func TestReadOnlyRegistersOnlyReadTools(t *testing.T) {
	_, reg, err := server.New(config.Config{Toolsets: []string{"all"}, ReadOnly: true}, server.Deps{}, All())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reg.Specs() {
		if s.Kind != server.KindRead {
			t.Errorf("%s (%s) зареєстровано в read-only", s.Name, s.Kind)
		}
	}
}

// TestToolsCloseResponses перевіряє код інструментів:
//   - методи …WithResponse заборонені: вони розбирають тіло в згенеровані типи
//     й падають, щойно реальна відповідь keyCRM розходиться з api.yaml;
//   - кожен виклик згенерованого клієнта (d.KeyCRM.API.X(ctx… або c.X(ctx…)
//     стоїть у тому ж рядку всередині listing.Raw( чи listing.Object(, які
//     читають і закривають тіло. Через це bodyclose для internal/tools і
//     internal/receipts вимкнено.
func TestToolsCloseResponses(t *testing.T) {
	typed := regexp.MustCompile(`\.\w+WithResponse\(`)
	call := regexp.MustCompile(`(listing\.(?:Raw|Object)\((?:\w+\.)*)?\b(?:API|c)\.[A-Z]\w*\(ctx\b`)
	// ../receipts — сервіс накладних теж викликає API напряму.
	for _, root := range []string{".", "../receipts"} {
		walkSources(t, root, typed, call)
	}
}

func walkSources(t *testing.T, root string, typed, call *regexp.Regexp) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path) //nolint:gosec // шлях з обходу власного пакета в тесті
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if typed.MatchString(line) {
				t.Errorf("%s:%d: метод WithResponse розбирає тіло в згенеровані типи; викликайте метод без суфікса через listing.Raw/Object", path, i+1)
			}
			for _, m := range call.FindAllStringSubmatch(line, -1) {
				if m[1] == "" {
					t.Errorf("%s:%d: виклик API поза listing.Raw/Object — тіло відповіді не буде закрито: %s", path, i+1, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
