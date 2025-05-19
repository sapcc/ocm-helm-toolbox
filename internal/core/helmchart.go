// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sapcc/go-bits/logg"

	"github.com/sapcc/ocm-helm-toolbox/internal/util"
)

// HelmChart contains some fields from Chart.yaml.
// Fields not used by this application are omitted.
type HelmChart struct {
	// the path where this chart resides in the filesystem
	ChartPath string `yaml:"-"`

	APIVersion   string                    `yaml:"apiVersion"`
	Name         string                    `yaml:"name"`
	Version      string                    `yaml:"version"`
	Dependencies []DeclaredChartDependency `yaml:"dependencies"`
}

// DeclaredChartDependency appears in Chart.yaml of a Helm chart.
type DeclaredChartDependency struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`

	// This field may contain a match expression like "^1.1" instead of a concrete version like "1.1.5".
	Version string `yaml:"version"`
}

// ComputedChartDependency appears in Chart.lock of a Helm chart.
type ComputedChartDependency struct {
	Name       string `yaml:"name"`
	Repository string `yaml:"repository"`

	// This field always contains a concrete version, never a match expression.
	Version string `yaml:"version"`
}

// ParseHelmChartYAML parses the Chart.yaml file below the given path.
func ParseHelmChartYAML(chartPath string) (HelmChart, error) {
	result, err := util.ReadYAMLFile[HelmChart](filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return HelmChart{}, err
	}
	result.ChartPath = chartPath
	return result, nil
}

// AddTimestampToVersion contains the logic for the `add-timestamp-to-version` subcommand.
func (c *HelmChart) AddTimestampToVersion() error {
	if strings.Contains(c.Version, "+") {
		return fmt.Errorf("Chart.yaml already has a build identifier (version = %q), cannot add another one", c.Version) //nolint:staticcheck // Chart.yaml is capitalized for a reason
	}

	oldVersion := c.Version
	newVersion := fmt.Sprintf("%s+bundle.%s", oldVersion, time.Now().Format("20060102-150405"))
	c.Version = newVersion

	// we don't want to destroy custom fields, comments etc. in Chart.yaml when editing it,
	// so we will try and edit only the line setting the version
	chartYAMLPath := filepath.Join(c.ChartPath, "Chart.yaml")
	buf, err := os.ReadFile(chartYAMLPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(buf), "\n")
	edited := false
	for idx, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "version") {
			continue
		}
		newLine := strings.ReplaceAll(line, oldVersion, newVersion)
		if newLine != line {
			lines[idx] = newLine
			edited = true
		}
	}

	if !edited {
		return fmt.Errorf("tried to edit Chart.yaml, but could not find a line that looks like `version = %q`", oldVersion)
	}

	logg.Info("Changed chart version from %q to %q", oldVersion, newVersion)
	buf = []byte(strings.Join(lines, "\n"))
	return os.WriteFile(chartYAMLPath, buf, 0666)
}

// AsOCMResource returns a resource declaration for this Helm chart.
func (c HelmChart) AsOCMResource() (OCMResourceDeclaration, error) {
	decl := OCMResourceDeclaration{
		Name:    "helm-chart-" + c.Name,
		Type:    "helmChart",
		Version: c.Version,
		Input: map[string]any{
			"type": "dir",
			"path": c.ChartPath,
		},
	}

	gitLocation, err := TryGetGitLocation(c.ChartPath)
	if err != nil {
		return OCMResourceDeclaration{}, err
	}
	if loc, ok := gitLocation.Unpack(); ok {
		buf, err := json.Marshal(loc)
		if err != nil {
			return OCMResourceDeclaration{}, err
		}
		decl.Labels = []OCMLabel{{
			Name:  GitLocationLabelName,
			Value: string(buf),
		}}
	}

	return decl, nil
}

// ValidateDependencies verifies that `helm dep build` has been run.
// If this is not the case, then bundling the chart might not include all relevant subcharts.
// Ref: <https://github.com/open-component-model/ocm/issues/1007>
func (c HelmChart) ValidateDependencies() error {
	// This will contain all the files that we expect directly below `charts/` as keys.
	expectedFiles := make(map[string]struct{})

	switch c.APIVersion {
	case "v1":
		// in v1, dependencies are declared in a different way
		// (using `requirements.{yaml,lock}` instead of `Chart.{yaml,lock}`)
		// which we don't bother to support
		return fmt.Errorf("cannot validate chart dependencies for %s with apiVersion: v1 (please upgrade to v2; see <%s> for details)",
			c.ChartPath, "https://helm.sh/docs/topics/charts/#the-apiversion-field",
		)
	case "v2":
		// ok
	default:
		return fmt.Errorf("cannot validate chart dependencies for %s with apiVersion: %s (this tool only supports v2)",
			c.ChartPath, c.APIVersion)
	}

	// if there are dependencies, Chart.lock will tell us the exact versions
	if len(c.Dependencies) > 0 {
		type chartLockContents struct {
			// NOTE: unused fields omitted
			Dependencies []ComputedChartDependency `yaml:"dependencies"`
		}
		chartLock, err := util.ReadYAMLFile[chartLockContents](filepath.Join(c.ChartPath, "Chart.lock"))
		if err != nil {
			return err
		}
		err = validateDependencyCoherence(c.Dependencies, chartLock.Dependencies)
		if err != nil {
			return fmt.Errorf("Chart.yaml and Chart.lock in %s do not agree: %w", c.ChartPath, err) //nolint:staticcheck // Chart.yaml is capitalized for a reason
		}

		for _, dep := range chartLock.Dependencies {
			fileName := fmt.Sprintf("%s-%s.tgz", dep.Name, dep.Version)
			expectedFiles[fileName] = struct{}{}
		}
	}

	// check the directory entries in `charts/` against our expectation
	entries, err := os.ReadDir(filepath.Join(c.ChartPath, "charts"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		relPath := filepath.Join("charts", entry.Name())
		if !entry.Type().IsRegular() {
			return fmt.Errorf("while validating subcharts of %s: expected only regular files, but %s is %s",
				c.ChartPath, relPath, entry.Type().String(),
			)
		}
		_, exists := expectedFiles[entry.Name()]
		if !exists {
			return fmt.Errorf("while validating subcharts of %s: found unexpected file %s", c.ChartPath, relPath)
		}
		delete(expectedFiles, entry.Name())
	}
	for fileName := range expectedFiles {
		return fmt.Errorf("while validating subcharts of %s: did not find expected file %s",
			c.ChartPath, filepath.Join("charts", fileName))
	}
	return nil
}

// Validate that the `dependencies` sections of Chart.yaml and Chart.lock agree with each other.
func validateDependencyCoherence(declaredDeps []DeclaredChartDependency, computedDeps []ComputedChartDependency) error {
	declaredSet := make(map[string]DeclaredChartDependency, len(declaredDeps))
	for _, dep := range declaredDeps {
		declaredSet[dep.Name] = dep
	}
	computedSet := make(map[string]ComputedChartDependency, len(computedDeps))
	for _, dep := range computedDeps {
		computedSet[dep.Name] = dep
	}

	for depName, declaredDep := range declaredSet {
		computedDep, exists := computedSet[depName]
		if !exists {
			return fmt.Errorf("Chart.yaml declares a dependency on %q, but Chart.lock does not have this dependency", depName) //nolint:staticcheck // Chart.yaml is capitalized for a reason
		}
		if computedDep.Repository != declaredDep.Repository {
			return fmt.Errorf("Chart.yaml declares dependency %q as coming from %s, but Chart.lock has it coming from %s", //nolint:staticcheck // Chart.yaml is capitalized for a reason
				depName, declaredDep.Repository, computedDep.Repository)
		}
		// TODO: validate that computedDep.Version matches declaredDep.Version
		//
		// (This is mostly relevant for interactive use, and thus omitted for now.
		// In CI, `helm dep build` categorically has to run to populate `charts/`,
		// so `helm dep build` will fail before us because of the contradiction.)

		delete(declaredSet, depName)
		delete(computedSet, depName)
	}

	for depName := range computedSet {
		return fmt.Errorf("Chart.lock declares a dependency on %q, but Chart.yaml does not have this dependency", depName) //nolint:staticcheck // Chart.lock is capitalized for a reason
	}

	return nil
}

// UnpackHelmChartTarball takes the binary contents of a chart.tar file and
// unpacks them into the given output path.
func UnpackHelmChartTarball(buf []byte, outputDirPath string) error {
	tr := tar.NewReader(bytes.NewReader(buf))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		logg.Debug("unpacking %s...", hdr.Name)
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("refusing to extract file %q which looks like it wants to exploit a path-traversal vulnerability", hdr.Name)
		}
		targetPath := filepath.Join(outputDirPath, hdr.Name) //nolint:gosec // yes, gosec, we do in fact want to unpack a tar archive in this unpack-tar-archive function
		err = os.MkdirAll(filepath.Dir(targetPath), 0777)    // NOTE: final mode is subject to umask
		if err != nil {
			return err
		}

		// NOTE: We disregard most file attributes here (permissions, ownership,
		// timestamps), since Helm only looks at file names and contents.
		switch hdr.Typeflag {
		case tar.TypeReg: // regular file
			writer, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			_, err = io.Copy(writer, tr) //nolint:gosec // what did I say, gosec!?
			if err != nil {
				defer writer.Close()
				return err
			}
			err = writer.Close()
			if err != nil {
				return err
			}
		case tar.TypeDir:
			err = os.Mkdir(targetPath, 0777) // NOTE: final mode is subject to umask
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("do not know how to extract non-regular file %q", targetPath)
		}
	}
}
