// SPDX-FileCopyrightText: 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/containers/image/v5/docker/reference"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/osext"
)

// ImageRelation contains a parsed `--image-relation` value.
type ImageRelation struct {
	// these fields are filled in parseImageRelation()
	TargetPath     string          `json:"target-path"` // which Helm value to overwrite
	Attribute      string          `json:"attribute"`   // one of: "repository", "digest", "tag", "reference"
	ImageReference reference.Named `json:"-"`
	// this field is filled during bundling
	ImageResourceName string `json:"image-resource-name"`
}

var (
	variableReferenceRx   = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
	commandSubstitutionRx = regexp.MustCompile(`\$\(([^)]*)\)`)
	imageRelationRx       = regexp.MustCompile(`^\.Values\.(\S+)\s+is\s+(repository|tag|digest|reference)\s+of\s+(\S+)$`)
)

func parseImageRelation(ctx context.Context, input string) (ImageRelation, error) {
	// resolve variable references
	var err error
	input, err = replaceUnlessError(variableReferenceRx, input, func(match []string) (string, error) {
		val, err := osext.NeedGetenv(match[1])
		return strings.TrimSpace(val), err
	})
	if err != nil {
		return ImageRelation{}, err
	}

	// resolve command substitutions
	input, err = replaceUnlessError(commandSubstitutionRx, input, func(match []string) (string, error) {
		if strings.ContainsAny(match[1], "()[]{}`\"'") {
			return "", fmt.Errorf("refusing to execute command %q which appears to contain shell syntax", match[1])
		}
		words := strings.Fields(match[1])
		if len(words) == 0 {
			return "", fmt.Errorf("refusing to execute command %q which contains no command", match[1])
		}
		logg.Debug("executing command %q with arguments %#v", words[0], words[1:])
		cmd := exec.CommandContext(ctx, words[0], words[1:]...) //nolint:gosec // I understand that this looks scary to you, gosec, but it's intended functionality
		cmd.Stdin = nil
		cmd.Stderr = os.Stderr
		buf, err := cmd.Output()
		return strings.TrimSpace(string(buf)), err
	})
	if err != nil {
		return ImageRelation{}, err
	}

	// parse relation
	match := imageRelationRx.FindStringSubmatch(input)
	if match == nil {
		return ImageRelation{}, fmt.Errorf("does not match expected format /%s/ (pre-processed input was %q)",
			imageRelationRx.String(), input)
	}

	// parse image reference
	named, err := reference.ParseNormalizedNamed(match[3])
	if err != nil {
		return ImageRelation{}, fmt.Errorf("%w (raw reference was %q)",
			err, match[3])
	}
	return ImageRelation{
		TargetPath:     match[1],
		Attribute:      match[2],
		ImageReference: named,
	}, nil
}

// Like Regexp.ReplaceAllStringFunc(), but propagates errors, and provides a full submatch list to the predicate.
func replaceUnlessError(rx *regexp.Regexp, input string, replace func([]string) (string, error)) (string, error) {
	var retainedError error
	result := rx.ReplaceAllStringFunc(input, func(matched string) string {
		if retainedError != nil {
			return matched // stop replacing after first error
		}
		replacement, err := replace(rx.FindStringSubmatch(matched))
		if err == nil {
			return replacement
		} else {
			retainedError = err
			return matched
		}
	})
	return result, retainedError
}

// GetValue reads the attribute named by the Attribute field from the image reference in the ImageReference field.
func (rel ImageRelation) GetValue() (string, error) {
	ref := rel.ImageReference
	switch rel.Attribute {
	case "reference":
		return ref.String(), nil
	case "repository":
		return ref.Name(), nil
	case "digest":
		digested, ok := ref.(reference.Digested)
		if ok {
			return digested.Digest().String(), nil
		}
	case "tag":
		tagged, ok := ref.(reference.Tagged)
		if ok {
			return tagged.Tag(), nil
		}
	}
	return "", fmt.Errorf("could not find attribute %q in image reference %q", rel.Attribute, ref.String())
}
