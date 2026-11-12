package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jmeiracorbal/docker-blue-green/internal/scaffold"
	"github.com/jmeiracorbal/docker-blue-green/internal/deploycli"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bluegreen:", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output io.Writer) error {
	if len(args)>0 && (args[0]=="deploy" || args[0]=="rollback" || args[0]=="secrets") {
		return deploycli.Run(args, output)
	}
	if len(args) == 0 || args[0] != "init" {
		return fmt.Errorf("usage: bluegreen init [--dir PATH] [--compose FILE] [--app SERVICE,...] [--infrastructure SERVICE,...]")
	}
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("dir", ".", "project directory")
	source := flags.String("compose", "", "existing Compose file")
	apps := flags.String("app", "", "comma-separated application services")
	infrastructure := flags.String("infrastructure", "", "comma-separated infrastructure services")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	sourcePath, found, err := scaffold.FindSource(root, *source)
	if err != nil {
		return err
	}
	var doc []byte
	if found {
		command := exec.Command("docker", "compose", "--project-directory", root, "-f", sourcePath, "config", "--format", "json", "--no-interpolate", "--no-env-resolution", "--no-path-resolution", "--no-normalize")
		command.Dir = root
		doc, err = command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("reading Compose configuration: %w: %s", err, strings.TrimSpace(string(doc)))
		}
	}
	generated, err := scaffold.Generate(scaffold.Options{
		Root: root, SourcePath: sourcePath, ComposeJSON: doc,
		AppServices: split(*apps), InfrastructureServices: split(*infrastructure),
		Input: input, Output: output,
	})
	if err != nil {
		return err
	}
	if err := scaffold.Write(root, generated); err != nil {
		return err
	}
	for _, file := range generated {
		fmt.Fprintln(output, file.Path)
	}
	return nil
}

func split(value string) []string {
	if value == "" {
		return nil
	}
	items := strings.Split(value, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}
