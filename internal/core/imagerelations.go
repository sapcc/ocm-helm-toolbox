// SPDX-FileCopyrightText: 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/containers/image/v5/docker/reference"
)

var imageRelationSeparatorRx = regexp.MustCompile(`,|\n`)

// ImageRelations is a set of parsed `--image-relation` value.
// This is the payload type for an "image-relations.json" file.
type ImageRelations []*ImageRelation

// ParseImageRelations parses the --image-relation options of the `bundle` subcommand.
func ParseImageRelations(ctx context.Context, inputs []string) (ImageRelations, error) {
	var result ImageRelations
	for _, input := range inputs {
		for _, in := range imageRelationSeparatorRx.Split(input, -1) {
			in = strings.TrimSpace(in)
			if in == "" {
				// allow e.g. trailing comma at the end of a list inside an --image-relation value
				continue
			}
			rel, err := parseImageRelation(ctx, in)
			if err != nil {
				return nil, fmt.Errorf("while parsing --image-relation %q: %w", in, err)
			}
			result = append(result, &rel)
		}
	}
	return result, nil
}

// AssignResourceNames fills the ImageResourceName field of each relation (where not done yet),
// such that there is a unique mapping between ImageResourceName and ImageReference.
func (rels ImageRelations) AssignResourceNames() {
	// check existing ImageResourceName assignments
	resNameForImageRef := make(map[string]string)
	hasResName := make(map[string]bool)
	for _, rel := range rels {
		resName := rel.ImageResourceName
		if resName != "" {
			resNameForImageRef[rel.ImageReference.String()] = resName
			hasResName[resName] = true
		}
	}

	// fill missing ImageResourceName assignments
	for _, rel := range rels {
		imageRef := rel.ImageReference.String()
		if resName, exists := resNameForImageRef[imageRef]; exists {
			rel.ImageResourceName = resName
			continue
		}

		fullRepoName := rel.ImageReference.Name() // e.g. "quay.io/prometheuscommunity/postgres_exporter"
		repoName := path.Base(fullRepoName)       // e.g. "postgres_exporter"

		// we prefer the resource name "image-${repoName}", but if that would not be unique,
		// we add "-1", "-2", etc. to disambiguate
		resName := "image-" + repoName
		counter := 0
		for {
			if hasResName[resName] {
				counter++
				resName = fmt.Sprintf("image-%s-%d", repoName, counter)
			} else {
				break
			}
		}

		rel.ImageResourceName = resName
		resNameForImageRef[imageRef] = resName
		hasResName[resName] = true
	}
}

// AsOCMResources renders this set of image relations into a set of OCM resource declarations, one for each referenced image
// Furthermore, the image relations are serialized into JSON.
// The "unbundle" subcommand wants to find these as the `cloud.sap/image-relations` label on the Helm chart resource.
//
// Images that do not have a tag will use the provided `bundleVersion` as their version string.
func (rels ImageRelations) AsOCMResources(bundleVersion string) (resources []OCMResourceDeclaration, imageRelationsJSON string, err error) {
	if len(rels) == 0 {
		return nil, "[]", nil
	}

	rels.AssignResourceNames()
	imageRefForResourceName := make(map[string]reference.Named, len(rels))
	for _, rel := range rels {
		imageRefForResourceName[rel.ImageResourceName] = rel.ImageReference
	}

	// serialize image-relations.json
	buf, err := json.Marshal(rels)
	if err != nil {
		return nil, "", fmt.Errorf("could not serialize image relations to JSON: %w", err)
	}

	// render one resource for each image
	for _, resName := range slices.Sorted(maps.Keys(imageRefForResourceName)) {
		imageRef := imageRefForResourceName[resName]
		version := bundleVersion
		if tagged, ok := imageRef.(reference.Tagged); ok {
			version = tagged.Tag()
		}

		resources = append(resources, OCMResourceDeclaration{
			Name:    resName,
			Type:    "ociImage",
			Version: version,
			Access: map[string]any{
				"type":           "ociArtifact",
				"imageReference": imageRef.String(),
			},
		})
	}
	return resources, string(buf), nil
}

// BuildLocalizedValues builds the contents of localized-values.yaml during unbundling.
func (rels ImageRelations) BuildLocalizedValues() (map[string]any, error) {
	out := make(map[string]any)
	for _, rel := range rels {
		value, err := rel.GetValue()
		if err != nil {
			return nil, err
		}
		err = insertIntoMapRecursively(out, rel.TargetPath, value)
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

func insertIntoMapRecursively(target map[string]any, targetPath string, value any) error {
	key, subpath, found := strings.Cut(targetPath, ".")
	if !found {
		target[key] = value
		return nil
	}

	switch subtarget := target[key].(type) {
	case map[string]any:
		return insertIntoMapRecursively(subtarget, subpath, value)
	case nil:
		newSubtarget := make(map[string]any)
		target[key] = newSubtarget
		return insertIntoMapRecursively(newSubtarget, subpath, value)
	default:
		return fmt.Errorf("cannot insert value into subpath %q of value with type %T", subpath, subtarget)
	}
}
