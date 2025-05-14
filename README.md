<!--
SPDX-FileCopyrightText: 2025 SAP SE

SPDX-License-Identifier: Apache-2.0
-->

# ocm-helm-toolbox

This is a toolbox for bundling [Helm charts](https://helm.sh) into OCM component versions (in CI),
and later unpacking from that format for a deployment (in CD).

Unlike other OCM-based workflows, this toolbox is specifically designed to not
require extra operators to be installed in the target Kubernetes cluster.
All that you need is a Helm CLI.

Staging area for the sapcc/ocm-helm-toolbox. Build with `make`.

## Installation

From the cloned repo, the application can be built with `make` and installed with `make install`.
The `helm` and `ocm` commands must be installed for the toolbox to do its work.

## Help

```console
$ ocm-helm-toolbox --help
Toolbox for deploying Helm charts with OCM

Usage:
  ocm-helm-toolbox [command]

Available Commands:
  add-timestamp-to-version Adds a build timestamp to the given chart's version.
  bundle                   Prepares a component constructor for a Helm chart.
  completion               Generate the autocompletion script for the specified shell
  help                     Help about any command
  unbundle                 Unpacks a Helm chart from an OCM component version.

Flags:
      --debug     print more detailed logs
  -h, --help      help for ocm-helm-toolbox
  -v, --version   version for ocm-helm-toolbox

Use "ocm-helm-toolbox [command] --help" for more information about a command.
```

```console
$ ocm-helm-toolbox add-timestamp-to-version --help
Adds a build timestamp to the given chart's version.

This is useful when you want to upload multiple bundles of a Helm chart
to an OCM store without having to bump the chart version for each change.

Usage:
  ocm-helm-toolbox add-timestamp-to-version <helm-chart-directory> [flags]

Flags:
  -h, --help   help for add-timestamp-to-version

Global Flags:
      --debug   print more detailed logs
```

```console
$ ocm-helm-toolbox bundle --help
Prepares a component constructor for the given Helm chart, for consumption by "ocm add componentversions".

To make the bundle hermetic, all images referenced by the Helm chart should be declared with --image-relation. For example:
    --image-relation ".Values.db_metrics.image.repository is repository of quay.io/prometheuscommunity/postgres_exporter:0.16.0"
    --image-relation ".Values.db_metrics.image.tag is tag of quay.io/prometheuscommunity/postgres_exporter:0.16.0"
    [and so on]

Images so declared as related to the Helm chart will be bundled into the OCM component version, and transported inside it.
On unbundle, a localized-values.yaml file will be rendered which overwrites the declared value paths to refer to the bundled images.

Usage:
  ocm-helm-toolbox bundle <helm-chart-directory> [flags]

Flags:
      --component-name-prefix string   (required) A prefix that will be prepended to the name of
                                       the first Helm chart to form the overall component name.
                                       Usually looks like a URL path element, e.g. "example.org/".
  -h, --help                           help for bundle
      --image-relation stringArray     A declaration of the form ".Values.<path> is <repository|digest|tag|reference> of <docker-image-ref>".
                                       See command documentation above for what this declaration causes.
                                       The option may be given multiple times to include multiple declarations.
                                       A single option may also contain multiple declarations, separated by commas.

                                       References to ${ENVIRONMENT_VARIABLES} in exactly this one form are replaced with the respective variable's value.
                                       After that, $(command substitutions) in exactly this one form are replaced by the output of the command.
                                       Command substitution does not understand any quoting or nested shell syntax.
                                       Only a list of bare words is supported, like "$(cat version.txt)".
      --provider-name string           (required) The provider name value for the component metadata.

Global Flags:
      --debug   print more detailed logs
```

```console
$ ocm-helm-toolbox unbundle --help
Unpacks a Helm chart from an OCM component version created by the "bundle" subcommand.

The component version can be given either as the path to a CTF archive on the filesystem,
or as a fully qualified reference into an OCI registry, in the form "$OCI_REGISTRY//$COMPONENT_NAME:$COMPONENT_VERSION".

If the component version contains image relations, a file "localized-values.yaml" is rendered
into the output directory. This file must be given to Helm with the --values switch.

If the Helm chart carries a "cloud.sap/git-location" label, its contents are written
into the output directory under the file name "git-location.json".

Usage:
  ocm-helm-toolbox unbundle <component-version> <target-directory> [flags]

Flags:
  -h, --help   help for unbundle

Global Flags:
      --debug   print more detailed logs
```
