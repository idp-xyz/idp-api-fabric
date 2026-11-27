// Command fabric-import imports the contract files of a folder as a revision
// of an API asset, through the management API (importRevision). It is what a
// CI job of a contract repository runs after a merge, so that what was merged
// is recorded with where it came from:
//
//	IDP_FABRIC_TOKEN=… fabric-import -api https://fabric.example \
//	    -product cargopai -asset shipments-api -dir contracts/shipments -git
//
// The product and the asset are named by their keys or their identifiers. It
// sends the YAML and JSON files under the folder, and none under a hidden
// folder; the server keeps the entrypoint and the files it references. The
// entrypoint is -entry, or else the openapi.yaml, .yml or .json at the top of
// the folder.
//
// With -git the source is declared as read from the repository the folder is
// in: the URL of its remote (https, without credentials and without ".git"),
// the commit checked out and, when there is one, the branch or tag; GITHUB_REF
// names it in GitHub Actions. The paths are then the paths in the repository.
// A folder whose files differ from the commit is refused, since the commit
// would not hold what is declared. Without -git the source is an upload.
//
// The access token is read from IDP_FABRIC_TOKEN and never from a flag, so
// that it stays out of the process list and the shell history. It is sent
// over https only, or over http to a loopback address. The answer is one line:
// the revision, and whether it was recorded now or before, as the server
// returns the revision it recorded first for files it already holds.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const requestTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "fabric-import:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("fabric-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	api := flags.String("api", getenv("IDP_FABRIC_API_URL"), "origin of the Fabric management API (default $IDP_FABRIC_API_URL)")
	product := flags.String("product", "", "key or identifier of the product")
	asset := flags.String("asset", "", "key or identifier of the API asset")
	dir := flags.String("dir", ".", "folder holding the contract files")
	entry := flags.String("entry", "", "entrypoint, relative to -dir (default: openapi.yaml, .yml or .json at its top)")
	git := flags.Bool("git", false, "declare the repository, commit and ref the files were read at")
	remote := flags.String("remote", "origin", "with -git, the remote whose URL names the repository")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *product == "" || *asset == "" {
		return errors.New("-product and -asset are required")
	}
	token := getenv("IDP_FABRIC_TOKEN")
	if token == "" {
		return errors.New("IDP_FABRIC_TOKEN is not set")
	}
	client, err := newClient(*api, token, &http.Client{Timeout: requestTimeout})
	if err != nil {
		return err
	}

	var origin *gitOrigin
	base := *dir
	if *git {
		repo, err := readOrigin(ctx, *dir, *remote, getenv)
		if err != nil {
			return err
		}
		origin, base = &repo.origin, repo.root
	}
	upload, err := collect(*dir, base, *entry)
	if err != nil {
		return err
	}
	if origin != nil {
		if err := committed(ctx, base, origin.Commit, upload.Files, os.ReadFile); err != nil {
			return err
		}
	}
	upload.GitOrigin = origin

	productID, err := client.productID(ctx, *product)
	if err != nil {
		return err
	}
	assetID, err := client.assetID(ctx, productID, *asset)
	if err != nil {
		return err
	}
	revision, created, err := client.importRevision(ctx, productID, assetID, upload)
	if err != nil {
		return err
	}
	verb := "recorded before"
	if created {
		verb = "recorded now"
	}
	_, err = fmt.Fprintf(stdout, "revision #%d %s %s, %s: entrypoint %s, %d files, %s\n",
		revision.Sequence, revision.ID, revision.Status, verb, revision.Entrypoint, len(upload.Files), revision.SourceDigest)
	return err
}
