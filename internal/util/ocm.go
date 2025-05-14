// SPDX-FileCopyrightText: 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/sapcc/go-bits/logg"
)

// ExecOCM executes the `ocm` command with the given arguments and returns its stdout.
func ExecOCM(args ...string) ([]byte, error) {
	logg.Debug("running ocm binary with arguments %#v", args)
	cmd := exec.Command("ocm", args...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	buf, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("while running ocm binary with arguments %#v: %w", args, err)
	}
	return buf, err
}
