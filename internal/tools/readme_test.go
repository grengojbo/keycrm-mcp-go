// SPDX-License-Identifier: AGPL-3.0-only

package tools

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/grengojbo/keycrm-mcp-go/internal/config"
	"github.com/grengojbo/keycrm-mcp-go/internal/server"
)

// TestReadmeListsAllTools перевіряє, що кожна група й кожен інструмент
// згадані в обох README, щоб документація не відставала від коду.
func TestReadmeListsAllTools(t *testing.T) {
	_, reg, err := server.New(config.Config{Toolsets: []string{"all"}}, server.Deps{}, All())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	root := os.DirFS("../..")
	for _, name := range []string{"README.md", "README.en.md"} {
		t.Run(name, func(t *testing.T) {
			b, err := fs.ReadFile(root, name)
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			for _, s := range All() {
				if !strings.Contains(text, "`"+s.Name+"`") {
					t.Errorf("група %s не згадана", s.Name)
				}
			}
			for _, s := range reg.Specs() {
				if !strings.Contains(text, "`"+s.Name+"`") {
					t.Errorf("інструмент %s не згаданий", s.Name)
				}
			}
		})
	}
}
