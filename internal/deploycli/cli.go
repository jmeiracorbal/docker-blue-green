package deploycli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmeiracorbal/docker-blue-green/internal/deploy"
)

func Run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: bluegreen deploy --release FILE | bluegreen rollback --project NAME")
	}
	switch args[0] {
	case "secrets":
		return checkSecrets(args[1:], out)
	case "deploy":
		flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		root := flags.String("dir", ".", "deployment directory")
		release := flags.String("release", "", "release manifest (JSON)")
		envFile := flags.String("env-file", "", "local Compose environment file")
		wait := flags.Int("wait-seconds", 120, "healthcheck timeout")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *release == "" || *wait < 1 {
			return errors.New("usage: bluegreen deploy --release FILE [--dir PATH] [--env-file FILE] [--wait-seconds N]")
		}
		r, err := deploy.ParseRelease(*release)
		if err != nil {
			return err
		}
		c, err := newController(*root, *envFile, time.Duration(*wait)*time.Second)
		if err != nil {
			return err
		}
		if err := c.Deploy(context.Background(), r); err != nil {
			return err
		}
		fmt.Fprintf(out, "Release %s is active for %s\n", r.Version, r.Project)
		return nil
	case "rollback":
		flags := flag.NewFlagSet("rollback", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		root := flags.String("dir", ".", "deployment directory")
		project := flags.String("project", "", "project identifier")
		envFile := flags.String("env-file", "", "local Compose environment file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *project == "" {
			return errors.New("usage: bluegreen rollback --project NAME [--dir PATH] [--env-file FILE]")
		}
		c, err := newController(*root, *envFile, 0)
		if err != nil {
			return err
		}
		if err := c.Rollback(context.Background(), *project); err != nil {
			return err
		}
		fmt.Fprintf(out, "Previous release restored for %s\n", *project)
		return nil
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func newController(root, envFile string, wait time.Duration) (deploy.Controller, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return deploy.Controller{}, err
	}
	if envFile != "" {
		if !filepath.IsAbs(envFile) {
			envFile = filepath.Join(abs, envFile)
		}
		info, err := os.Stat(envFile)
		if err != nil {
			return deploy.Controller{}, err
		}
		if info.IsDir() {
			return deploy.Controller{}, fmt.Errorf("env-file is a directory: %s", envFile)
		}
	}
	if strings.TrimSpace(abs) == "" {
		return deploy.Controller{}, errors.New("invalid working directory")
	}
	return deploy.Controller{Root: abs, EnvFile: envFile, WaitTimeout: wait}, nil
}
