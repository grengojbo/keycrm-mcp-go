// SPDX-License-Identifier: AGPL-3.0-only

package keycrm

import (
	_ "embed"
	"errors"
	"fmt"
	"regexp"
)

//go:embed api.yaml
var specYAML []byte

// operationIDLine знаходить рядки `operationId: <id>` у вендореній специфікації.
var operationIDLine = regexp.MustCompile(`(?m)^\s+operationId:\s*['"]?([A-Za-z0-9_]+)['"]?\s*$`)

// SpecOperationIDs повертає множину operationId з вбудованого api.yaml.
// Контрактний тест інструментів звіряє з нею кожен виклик API.
func SpecOperationIDs() (map[string]struct{}, error) {
	matches := operationIDLine.FindAllSubmatch(specYAML, -1)
	if len(matches) == 0 {
		return nil, errors.New("keycrm: no operationId found in api.yaml")
	}
	ids := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		id := string(m[1])
		if _, dup := ids[id]; dup {
			return nil, fmt.Errorf("keycrm: duplicate operationId %q in api.yaml", id)
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}
