// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/majewsky/gg/option"
)

// GitLocation describes where a given directory from a checkout can be found on a Git server.
//
// The JSON serialization is for the `cloud.sap/git-location` label on the Helm chart resource.
// It is made to match https://pkg.go.dev/github.com/sapcc/go-api-declarations/deployevent#GitRepo for easy compatibility with concourse-release-resource.
type GitLocation struct {
	AuthoredAt    Option[time.Time] `json:"authored-at"`
	BranchName    string            `json:"branch"`
	CommittedAt   Option[time.Time] `json:"committed-at"`
	CommitID      string            `json:"commit-id"`
	RepositoryURL string            `json:"remote-url"`
	DirectoryPath string            `json:"subpath,omitempty"`
}

// TryGetGitLocation returns the GitLocation of the given directory, if it is
// inside a checkout of a Git repository, or None otherwise.
func TryGetGitLocation(path string) (Option[GitLocation], error) {
	// are we in a Git repository at all?
	out, err := execGitInPath(path, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if errors.Is(err, errNotAGitRepository) {
			err = nil
		}
		return None[GitLocation](), err
	}
	if strings.TrimSpace(out) != "true" {
		return None[GitLocation](), nil
	}

	// get information about HEAD commit
	out, err = execGitInPath(path, "show", "-s", "--pretty=%H %at %ct", "HEAD")
	if err != nil {
		return None[GitLocation](), err
	}
	fields := strings.Fields(out)
	if len(fields) != 3 {
		return None[GitLocation](), fmt.Errorf("malformed input from `git show --pretty='%%H %%at %%ct' HEAD`: %q", strings.TrimSpace(out))
	}
	authoredTimestamp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return None[GitLocation](), fmt.Errorf("malformed input from `git show --pretty='%%H %%at %%ct' HEAD`: %w", err)
	}
	committedTimestamp, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return None[GitLocation](), fmt.Errorf("malformed input from `git show --pretty='%%H %%at %%ct' HEAD`: %w", err)
	}
	result := GitLocation{
		CommitID:    fields[0],
		AuthoredAt:  Some(time.Unix(authoredTimestamp, 0)),
		CommittedAt: Some(time.Unix(committedTimestamp, 0)),
	}

	// get name of branch containing HEAD commit (the "if upstream" match drops the "detached HEAD" line, if there is one)
	outputFormat := "%(if)%(upstream)%(then)%(refname:short)%(end)"
	out, err = execGitInPath(path, "branch", "--contains", "HEAD", "--format="+outputFormat, "--omit-empty")
	if err != nil {
		return None[GitLocation](), err
	}
	fields = strings.Fields(out)
	if len(fields) != 0 {
		result.BranchName = fields[0]
	}

	// get path within working tree
	out, err = execGitInPath(path, "rev-parse", "--show-prefix")
	if err != nil {
		return None[GitLocation](), err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		result.DirectoryPath = ""
	} else {
		result.DirectoryPath = filepath.Clean(out)
	}

	// get repository URL from remote "origin"
	//
	// This fails if the remotes are set up differently, but if they are not,
	// we do not have a good basis for choosing the main upstream URL anyway.
	out, err = execGitInPath(path, "remote", "get-url", "origin")
	if err != nil {
		return None[GitLocation](), err
	}
	result.RepositoryURL = strings.TrimSpace(out)

	return Some(result), nil
}

var errNotAGitRepository = errors.New("not a Git repository")

func execGitInPath(path string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...) //nolint:gosec // args not controlled by user
	var buf bytes.Buffer
	cmd.Stderr = &buf

	out, err := cmd.Output()
	switch {
	case err == nil:
		return string(out), nil
	case strings.Contains(buf.String(), "not a git repository"):
		// This match is somewhat brittle, but I did not find a more resilient way.
		// On the plus side, if it ever breaks, we should notice very immediately
		// because the alternative is the loud error below.
		return "", errNotAGitRepository
	default:
		os.Stderr.Write(buf.Bytes()) // forward error output from Git to our stderr
		return "", fmt.Errorf(
			"could not run `git -C %q %s`: %w (stdout was %q)",
			path, strings.Join(args, " "), err, string(out),
		)
	}
}
