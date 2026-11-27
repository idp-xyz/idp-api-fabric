package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// The forms importRevision takes (GitRepository, GitCommit, GitRef).
	repositoryURL = regexp.MustCompile(`^https://[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*(:([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5]))?(/[A-Za-z0-9._~-]*[A-Za-z0-9_~-][A-Za-z0-9._~-]*)+$`)
	commitID      = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	gitRef        = regexp.MustCompile(`^refs/[^\p{Z}\p{Cc}]+$`)
	scpLike       = regexp.MustCompile(`^[^@/:]+@([^:/]+):(.+)$`)
)

type repository struct {
	root   string
	origin gitOrigin
}

func git(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Stdin = stdin
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// readOrigin finds the repository dir is in and what was checked out of it.
func readOrigin(ctx context.Context, dir, remote string, getenv func(string) string) (repository, error) {
	var repo repository
	top, err := git(ctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return repo, fmt.Errorf("-git: %s is not in a Git repository: %w", dir, err)
	}
	repo.root = strings.TrimSpace(string(top))
	head, err := git(ctx, repo.root, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return repo, fmt.Errorf("-git: nothing is checked out: %w", err)
	}
	repo.origin.Commit = strings.TrimSpace(string(head))
	if !commitID.MatchString(repo.origin.Commit) {
		return repo, fmt.Errorf("-git: %q is not a commit identifier", repo.origin.Commit)
	}
	address, err := git(ctx, repo.root, nil, "remote", "get-url", remote)
	if err != nil {
		return repo, fmt.Errorf("-git: the repository has no remote %q: %w", remote, err)
	}
	if repo.origin.Repository, err = repositoryOf(strings.TrimSpace(string(address))); err != nil {
		return repo, fmt.Errorf("-git: remote %q: %w", remote, err)
	}
	repo.origin.Ref = refOf(ctx, repo.root, getenv)
	return repo, nil
}

// repositoryOf writes a remote as importRevision takes a repository: the https
// URL of its host and path, without credentials, a port of SSH or ".git". An
// SSH remote is taken to be served over https by the same host under the same
// path, as GitHub and GitLab do.
func repositoryOf(remote string) (string, error) {
	if match := scpLike.FindStringSubmatch(remote); match != nil {
		remote = "ssh://" + match[1] + "/" + match[2]
	}
	parsed, err := url.Parse(remote)
	if err != nil {
		return "", fmt.Errorf("%q is not a URL", redact(remote))
	}
	host := strings.ToLower(parsed.Hostname())
	switch parsed.Scheme {
	case "https":
		if port := parsed.Port(); port != "" {
			host += ":" + port
		}
	case "ssh":
	default:
		return "", fmt.Errorf("%q is neither an https nor an SSH remote", redact(remote))
	}
	path := strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), ".git")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	repository := "https://" + host + path
	if parsed.RawQuery != "" || parsed.Fragment != "" || len(repository) > 300 || !repositoryURL.MatchString(repository) {
		return "", fmt.Errorf("%q does not name a repository as an https URL", redact(remote))
	}
	return repository, nil
}

// redact keeps the credentials a CI remote may carry out of a message.
func redact(remote string) string {
	if parsed, err := url.Parse(remote); err == nil && parsed.User != nil {
		parsed.User = url.User("***")
		return parsed.String()
	}
	return remote
}

// refOf names the branch or tag that was checked out: GITHUB_REF in GitHub
// Actions, else the branch, else a tag at the commit. A detached checkout of
// neither has none, and the origin is declared without it.
func refOf(ctx context.Context, root string, getenv func(string) string) string {
	if ref := getenv("GITHUB_REF"); gitRef.MatchString(ref) && len(ref) <= 200 {
		return ref
	}
	if branch, err := git(ctx, root, nil, "symbolic-ref", "-q", "HEAD"); err == nil {
		if ref := strings.TrimSpace(string(branch)); gitRef.MatchString(ref) && len(ref) <= 200 {
			return ref
		}
	}
	if tags, err := git(ctx, root, nil, "tag", "--points-at", "HEAD"); err == nil {
		if tag, _, _ := strings.Cut(strings.TrimSpace(string(tags)), "\n"); tag != "" {
			if ref := "refs/tags/" + tag; gitRef.MatchString(ref) && len(ref) <= 200 {
				return ref
			}
		}
	}
	return ""
}

// committed refuses files that differ from the commit, byte for byte: the
// origin declares that the commit holds what is sent.
func committed(ctx context.Context, root, commit string, files []sourceFile, read func(string) ([]byte, error)) error {
	var names strings.Builder
	for _, file := range files {
		names.WriteString(commit + ":" + file.Path + "\n")
	}
	out, err := git(ctx, root, strings.NewReader(names.String()), "cat-file", "--batch")
	if err != nil {
		return err
	}
	blobs := bufio.NewReader(bytes.NewReader(out))
	for _, file := range files {
		header, err := blobs.ReadString('\n')
		if err != nil {
			return errors.New("-git: git cat-file answered short")
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[1] != "blob" {
			return fmt.Errorf("-git: %s is not in commit %s; commit it first", file.Path, commit[:12])
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return errors.New("-git: git cat-file answered in a form it does not use")
		}
		blob := make([]byte, size+1)
		if _, err := io.ReadFull(blobs, blob); err != nil {
			return errors.New("-git: git cat-file answered short")
		}
		local, err := read(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		if !bytes.Equal(blob[:size], local) {
			return fmt.Errorf("-git: %s differs from commit %s; commit it first", file.Path, commit[:12])
		}
	}
	return nil
}
