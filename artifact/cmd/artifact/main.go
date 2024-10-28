// Package main provides the artifact CLI for importing and exporting artifacts.
package main

import (
	"bytes"
	"context"

	"github.com/edaniels/golog"
	"github.com/fatih/color"
	"github.com/pkg/errors"

	"go.viam.com/utils"
	"go.viam.com/utils/artifact/tools"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var logger utils.ZapCompatibleLogger = golog.NewDevelopmentLogger("artifact")

type topArguments struct {
	Command string   `flag:"0,required,usage=<clean|pull|push|rm|status>"`
	Extra   []string `flag:",extra"` // for sub-commands
}

type pullArguments struct {
	All      bool   `flag:"all,usage=pull all files regardless of size"`
	TreePath string `flag:"0,usage=pull a specific path from the tree in"`
}

type removeArguments struct {
	Path string `flag:"0,required,usage=rm <path>"`
}

const (
	commandNameClean  = "clean"
	commandNamePull   = "pull"
	commandNamePush   = "push"
	commandNameRemove = "rm"
	commandNameStatus = "status"
)

func mainWithArgs(ctx context.Context, args []string, logger utils.ZapCompatibleLogger) (err error) {
	var topArgsParsed topArguments
	if err := utils.ParseFlags(args, &topArgsParsed); err != nil {
		return err
	}
	switch topArgsParsed.Command {
	case commandNameClean:
		//nolint:contextcheck
		if err := tools.Clean(); err != nil {
			logger.Fatal(err)
		}
	case commandNamePull:
		var pullArgsParsed pullArguments
		if err := utils.ParseFlags(utils.StringSliceRemove(args, 1), &pullArgsParsed); err != nil {
			return err
		}
		//nolint:contextcheck
		if err := tools.Pull(pullArgsParsed.TreePath, pullArgsParsed.All); err != nil {
			logger.Fatal(err)
		}
	case commandNamePush:
		//nolint:contextcheck
		if err := tools.Push(); err != nil {
			logger.Fatal(err)
		}
	case commandNameRemove:
		var removeArgsParsed removeArguments
		if err := utils.ParseFlags(utils.StringSliceRemove(args, 1), &removeArgsParsed); err != nil {
			return err
		}
		//nolint:contextcheck
		if err := tools.Remove(removeArgsParsed.Path); err != nil {
			logger.Fatal(err)
		}
	case commandNameStatus:
		//nolint:contextcheck
		status, err := tools.Status()
		if err != nil {
			logger.Fatal(err)
		}
		var buf bytes.Buffer
		if len(status.Modified) != 0 {
			buf.WriteString("Modified:")
			yellowColor := color.New(color.FgYellow)
			for _, name := range status.Modified {
				buf.WriteString("\n\t")
				if _, err := yellowColor.Fprint(&buf, name); err != nil {
					logger.Fatal(err)
				}
			}
		}
		if len(status.Unstored) != 0 {
			if len(status.Modified) != 0 {
				buf.WriteString("\n")
			}
			buf.WriteString("Unstored:")
			redColor := color.New(color.FgRed)
			for _, name := range status.Unstored {
				buf.WriteString("\n\t")
				if _, err := redColor.Fprint(&buf, name); err != nil {
					logger.Fatal(err)
				}
			}
		}
		if buf.Len() != 0 {
			logger.Info("\n" + buf.String())
		}
	default:
		return errors.New("usage: artifact <clean|pull|push|rm|status>")
	}
	return nil
}
