// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ReadYAMLFile is os.ReadFile + yaml.Unmarshal.
func ReadYAMLFile[T any](path string) (data T, err error) {
	var buf []byte
	buf, err = os.ReadFile(path)
	if err != nil {
		return
	}
	err = yaml.Unmarshal(buf, &data)
	if err != nil {
		err = fmt.Errorf("while parsing %s: %w", path, err)
		return
	}
	return data, nil
}
