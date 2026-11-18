// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatalf("run(--version) повернув помилку: %v", err)
	}
	if got, want := out.String(), "keycrm-mcp dev\n"; got != want {
		t.Fatalf("вивід = %q, очікувано %q", got, want)
	}
}

func TestUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--no-such-flag"}, &out); err == nil {
		t.Fatal("очікувалась помилка для невідомого прапорця")
	}
}
