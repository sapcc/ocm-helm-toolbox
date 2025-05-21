// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containers/image/v5/docker/reference"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-bits/httpext"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/sapcc/ocm-helm-toolbox/internal/core"
)

func main() {
	cmd := &cobra.Command{
		Use:           "ocm-helm-toolbox",
		Short:         "Toolbox for deploying Helm charts with OCM",
		Args:          cobra.NoArgs,
		Version:       bininfo.VersionOr("dev"),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.PersistentFlags().BoolVar(&logg.ShowDebug, "debug", false, "print more detailed logs")
	cmd.AddCommand(addTimestampToVersionCmd())
	cmd.AddCommand(bundleCmd())
	cmd.AddCommand(unbundleCmd())

	// using a short timeout is acceptable here since this process is not a server
	ctx := httpext.ContextWithSIGINT(context.Background(), 100*time.Millisecond)
	must.Succeed(cmd.ExecuteContext(ctx))
}

func docstring(lines ...string) string {
	return strings.Join(lines, "\n")
}

////////////////////////////////////////////////////////////////////////////////
// subcommand: add-timestamp-to-version

func addTimestampToVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-timestamp-to-version <helm-chart-directory>",
		Short: "Adds a build timestamp to the given chart's version.",
		Long: docstring(
			`Adds a build timestamp to the given chart's version.`,
			``,
			`This is useful when you want to upload multiple bundles of a Helm chart`,
			`to an OCM store without having to bump the chart version for each change.`,
		),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chart, err := core.ParseHelmChartYAML(args[0])
			if err != nil {
				return err
			}
			return chart.AddTimestampToVersion()
		},
	}

	return cmd
}

////////////////////////////////////////////////////////////////////////////////
// subcommand: bundle

type bundleOpts struct {
	ComponentNamePrefix string
	ProviderName        string
	RawImageRelations   []string
}

func bundleCmd() *cobra.Command {
	var opts bundleOpts
	cmd := &cobra.Command{
		Use:   "bundle <helm-chart-directory>",
		Short: "Prepares a component constructor for a Helm chart.",
		Long: docstring(
			`Prepares a component constructor for the given Helm chart, for consumption by "ocm add componentversions".`,
			``,
			`To make the bundle hermetic, all images referenced by the Helm chart should be declared with --image-relation. For example:`,
			`    --image-relation ".Values.db_metrics.image.repository is repository of quay.io/prometheuscommunity/postgres_exporter:0.16.0"`,
			`    --image-relation ".Values.db_metrics.image.tag is tag of quay.io/prometheuscommunity/postgres_exporter:0.16.0"`,
			`    [and so on]`,
			``,
			`Images so declared as related to the Helm chart will be bundled into the OCM component version, and transported inside it.`,
			`On unbundle, a localized-values.yaml file will be rendered which overwrites the declared value paths to refer to the bundled images.`,
		),
		Args: cobra.ExactArgs(1), // TODO: support bundling multiple helm-charts that need to be installed in order (e.g. gatekeeper -> gatekeeper-config)
		RunE: opts.Run,
	}

	cmd.Flags().StringVar(&opts.ComponentNamePrefix, "component-name-prefix", "", docstring(
		`(required) A prefix that will be prepended to the name of`,
		`the first Helm chart to form the overall component name.`,
		`Usually looks like a URL path element, e.g. "example.org/".`,
	))
	cmd.Flags().StringVar(&opts.ProviderName, "provider-name", "",
		`(required) The provider name value for the component metadata.`,
	)
	cmd.Flags().StringArrayVar(&opts.RawImageRelations, "image-relation", nil, docstring(
		`A declaration of the form ".Values.<path> is <repository|digest|tag|reference> of <docker-image-ref>".`,
		`See command documentation above for what this declaration causes.`,
		`The option may be given multiple times to include multiple declarations.`,
		`A single option may also contain multiple declarations, separated by commas.`,
		``,
		`References to ${ENVIRONMENT_VARIABLES} in exactly this one form are replaced with the respective variable's value.`,
		`After that, $(command substitutions) in exactly this one form are replaced by the output of the command.`,
		`Command substitution does not understand any quoting or nested shell syntax.`,
		`Only a list of bare words is supported, like "$(cat version.txt)".`,
	))
	return cmd
}

func (opts *bundleOpts) Run(cmd *cobra.Command, args []string) error {
	if opts.ComponentNamePrefix == "" {
		return errors.New("no value provided for --component-name-prefix")
	}
	if opts.ProviderName == "" {
		return errors.New("no value provided for --provider-name")
	}

	// prepare OCM resource for the Helm chart
	chart, err := core.ParseHelmChartYAML(args[0])
	if err != nil {
		return err
	}
	err = chart.ValidateDependencies()
	if err != nil {
		return err
	}
	chartResource, err := chart.AsOCMResource()
	if err != nil {
		return err
	}

	// prepare OCM resources for related images
	rels, err := core.ParseImageRelations(cmd.Context(), opts.RawImageRelations)
	if err != nil {
		return err
	}
	imageResources, imageRelationsJSON, err := rels.AsOCMResources(chart.Version)
	if err != nil {
		return err
	}
	chartResource.Labels = append(chartResource.Labels, core.OCMLabel{
		Name:  core.ImageRelationsLabelName,
		Value: imageRelationsJSON,
	})

	// render component-constructor.yaml
	component := core.OCMComponentDeclaration{
		Name:      opts.ComponentNamePrefix + chart.Name,
		Version:   chart.Version,
		Provider:  map[string]any{"name": opts.ProviderName},
		Resources: append([]core.OCMResourceDeclaration{chartResource}, imageResources...),
	}
	buf, err := yaml.Marshal(map[string]any{"components": []core.OCMComponentDeclaration{component}})
	if err != nil {
		return fmt.Errorf("while marshaling component-constructor.yaml: %w", err)
	}
	fmt.Print(string(buf))
	return nil
}

///////////////////////////////////////////////////////////////////////////////////////////
// subcommand: unbundle

func unbundleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unbundle <component-version> <target-directory>",
		Short: "Unpacks a Helm chart from an OCM component version.",
		Long: docstring(
			`Unpacks a Helm chart from an OCM component version created by the "bundle" subcommand.`,
			``,
			`The component version can be given either as the path to a CTF archive on the filesystem,`,
			`or as a fully qualified reference into an OCI registry, in the form "$OCI_REGISTRY//$COMPONENT_NAME:$COMPONENT_VERSION".`,
			``,
			`If the component version contains image relations, a file "localized-values.yaml" is rendered`,
			`into the output directory. This file must be given to Helm with the --values switch.`,
			``,
			fmt.Sprintf(`If the Helm chart carries a %q label, its contents are written`, core.GitLocationLabelName),
			`into the output directory under the file name "git-location.json".`,
		),
		Args: cobra.ExactArgs(2), // TODO: support component versions containing multiple Helm charts (by taking multiple target dirs)
		RunE: unbundle,
	}
}

func unbundle(cmd *cobra.Command, args []string) error {
	// enumerate resources in this component version
	componentVersionRef := args[0]
	if componentVersionRef == "" {
		return errors.New("missing component version")
	}
	resources, err := core.GetOCMResources(componentVersionRef)
	if err != nil {
		return err
	}

	// prepare output directory
	outputDirPath := args[1]
	if outputDirPath == "" {
		return errors.New("missing output directory path")
	}
	err = os.MkdirAll(outputDirPath, 0777) // NOTE: final mode is subject to umask
	if err != nil {
		return err
	}

	// unpack the Helm chart
	res, err := resources.FindExactlyOneWith(`type: "helmChart"`, func(res core.OCMResourceInfo) bool {
		return res.Type == "helmChart"
	})
	if err != nil {
		return err
	}
	buf, err := res.GetPayloadFrom(componentVersionRef)
	if err != nil {
		return err
	}
	chartPath := filepath.Join(outputDirPath, strings.TrimPrefix(res.Name, "helm-chart-"))
	err = core.UnpackHelmChartTarball(buf, chartPath)
	if err != nil {
		return fmt.Errorf("could not unpack resource %q: %w", res.Name, err)
	}

	// parse image-relations.json
	resLabels := make(map[core.OCMLabelName]any, len(res.Labels))
	for _, label := range res.Labels {
		resLabels[label.Name] = label.Value
	}
	relationsValue, ok := resLabels[core.ImageRelationsLabelName]
	if !ok {
		return fmt.Errorf("could not unpack resource %q: missing required label %q",
			res.Name, core.ImageRelationsLabelName)
	}
	relationsJSON, ok := relationsValue.(string)
	if !ok {
		return fmt.Errorf("could not read label %q on resource %q: expected string value, but got %#v",
			core.ImageRelationsLabelName, res.Name, relationsValue)
	}
	var rels core.ImageRelations
	err = json.Unmarshal([]byte(relationsJSON), &rels)
	if err != nil {
		return fmt.Errorf("could not read label %q on resource %q: %w", core.ImageRelationsLabelName, res.Name, err)
	}

	// in image relations, resolve ImageResourceName back into ImageReference
	for _, rel := range rels {
		resName := rel.ImageResourceName
		res, err := resources.FindExactlyOneWith(fmt.Sprintf("name: %q", resName), func(res core.OCMResourceInfo) bool {
			return res.Name == resName
		})
		if err != nil {
			return fmt.Errorf("while resolving image relations: %w", err)
		}
		if res.Type != "ociImage" || res.Access.Type != "ociArtifact" || res.Access.ImageReference == "" {
			return fmt.Errorf("while resolving image relations: resource %q does not contain an OCI image reference", res.Name)
		}
		rel.ImageReference, err = reference.ParseNormalizedNamed(res.Access.ImageReference)
		if err != nil {
			return fmt.Errorf("could not parse image reference %q in resource %q: %w", res.Access.ImageReference, res.Name, err)
		}
	}

	// render localized-values.yaml
	localizedValues, err := rels.BuildLocalizedValues()
	if err != nil {
		return fmt.Errorf("could not build localized-values.yaml: %w", err)
	}
	buf, err = yaml.Marshal(localizedValues)
	if err != nil {
		return fmt.Errorf("could not marshal localized-values.yaml: %w", err)
	}
	localizedValuesPath := filepath.Join(outputDirPath, "localized-values.yaml")
	err = os.WriteFile(localizedValuesPath, buf, 0666) // NOTE: final mode is subject to umask
	if err != nil {
		return err
	}

	// render git-metadata.json (for consumption by concourse-release-resource)
	gitLocationValue, ok := resLabels[core.GitLocationLabelName]
	if ok {
		gitLocationJSON, ok := gitLocationValue.(string)
		if ok {
			gitLocationPath := filepath.Join(outputDirPath, "git-location.json")
			err = os.WriteFile(gitLocationPath, []byte(gitLocationJSON), 0666) // NOTE: final mode is subject to umask
			if err != nil {
				return err
			}
		}
	}

	return nil
}
