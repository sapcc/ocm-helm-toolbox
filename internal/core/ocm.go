// SPDX-FileCopyrightText: 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"fmt"

	"github.com/sapcc/ocm-helm-toolbox/internal/util"
)

// OCMComponentDeclaration is the `components[]` section of a component-constructor.yaml file.
//
// This is a heavily abridged type declaration that only contains the fields we need.
type OCMComponentDeclaration struct {
	Name      string                   `yaml:"name"`
	Version   string                   `yaml:"version"`
	Provider  map[string]any           `yaml:"provider"`
	Resources []OCMResourceDeclaration `yaml:"resources"`
}

// OCMResourceDeclaration is the `components[].resources[]` section of a component-constructor.yaml file.
//
// This is a heavily abridged type declaration that only contains the fields we need.
type OCMResourceDeclaration struct {
	Name    string         `yaml:"name"`
	Type    string         `yaml:"type"`
	Version string         `yaml:"version"`
	Labels  []OCMLabel     `yaml:"labels,omitempty"`
	Access  map[string]any `yaml:"access,omitempty"`
	Input   map[string]any `yaml:"input,omitempty"`
}

// OCMLabel is the `components[].resources[].labels[]` section of a component-constructor.yaml file.
//
// It is also used elsewhere, but we only care about resource labels.
// This is a heavily abridged type declaration that only contains the fields we need.
type OCMLabel struct {
	Name  OCMLabelName `yaml:"name"`
	Value any          `yaml:"value"` // type is intentional; they REALLY allow arbitrary YAML here
}

// OCMLabelName enumerates known OCMLabel names.
type OCMLabelName string

const (
	GitLocationLabelName    OCMLabelName = "cloud.sap/git-location"
	ImageRelationsLabelName OCMLabelName = "cloud.sap/image-relations"
)

// OCMResourceInfoSet contains information about several resources,
// as reported by `ocm get resources -o json`.
type OCMResourceInfoSet []OCMResourceInfo

// GetOCMResources lists the resources in the given component version.
func GetOCMResources(componentVersionRef string) (OCMResourceInfoSet, error) {
	buf, err := util.ExecOCM("get", "resources", "-o", "json", componentVersionRef)
	if err != nil {
		return nil, err
	}

	var data struct {
		Items []struct {
			Element OCMResourceInfo `json:"element"`
		} `json:"items"`
	}
	err = json.Unmarshal(buf, &data)
	if err != nil {
		return nil, fmt.Errorf("could not unpack output from `ocm get resources -o json`: %w", err)
	}

	result := make([]OCMResourceInfo, len(data.Items))
	for idx, item := range data.Items {
		result[idx] = item.Element
	}
	return result, nil
}

// FindExactlyOneWith returns the only resource that matches the predicate.
// If none or multiple resource match the predicate, an error is constructed using the provided description.
func (r OCMResourceInfoSet) FindExactlyOneWith(description string, match func(OCMResourceInfo) bool) (OCMResourceInfo, error) {
	var result []OCMResourceInfo
	for _, res := range r {
		if match(res) {
			result = append(result, res)
		}
	}
	switch len(result) {
	case 0:
		return OCMResourceInfo{}, fmt.Errorf("did not find any resource with %s", description)
	case 1:
		return result[0], nil
	default:
		return OCMResourceInfo{}, fmt.Errorf("expected 1 resource with %s, but found %d matching resources", description, len(result))
	}
}

// OCMResourceInfo contains information about an existing resource,
// as reported by `ocm get resources -o json`.
//
// This is a heavily abridged type declaration that only contains the fields we need.
type OCMResourceInfo struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Type    string            `json:"type"` // e.g. "file" or "helmChart" or "ociArtifact"
	Labels  []OCMLabel        `yaml:"labels,omitempty"`
	Access  OCMResourceAccess `json:"access"`
}

// GetPayloadFrom retrieves the resource's payload from the store holding the component version.
func (r OCMResourceInfo) GetPayloadFrom(componentVersionRef string) ([]byte, error) {
	buf, err := util.ExecOCM(
		"download", "resource", "-O", "-",
		componentVersionRef, r.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("could not download resource %q: %w", r.Name, err)
	}
	return buf, nil
}

// OCMResourceAccess appears in type OCMResourceInfo.
//
// This is a heavily abridged type declaration that only contains the fields we need.
type OCMResourceAccess struct {
	Type           string `json:"type"`           // e.g. "localBlob" or "ociArtifact"
	ImageReference string `json:"imageReference"` // only for .Type == "ociArtifact"
	MediaType      string `json:"mediaType"`      // only for .Type == "localBlob"
	LocalReference string `json:"localReference"` // only for .Type == "localBlob"
}
