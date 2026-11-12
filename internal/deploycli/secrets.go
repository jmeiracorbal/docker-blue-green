package deploycli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

func checkSecrets(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: bluegreen secrets check --project NAME --socket PATH [--dir PATH]")
	}
	flags := flag.NewFlagSet("secrets check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("dir", ".", "project deployment directory")
	project := flags.String("project", "", "deployment project name")
	socket := flags.String("socket", "", "OpenBao Proxy Unix socket")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *project == "" || *socket == "" {
		return errors.New("usage: bluegreen secrets check --project NAME --socket PATH [--dir PATH]")
	}
	manifest, err := openbao.Load(filepath.Join(*root, "deploy", "secrets.json"), *project)
	if err != nil {
		return err
	}
	client, err := openbao.NewClient(*socket)
	if err != nil {
		return err
	}
	if err := client.Check(context.Background(), *project, manifest); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%d OpenBao secret references verified for %s\n", len(manifest.Secrets), *project)
	return err
}
