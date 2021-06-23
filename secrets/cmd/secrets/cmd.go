package main

import (
	"context"

	"github.com/edaniels/golog"

	"go.viam.com/utils"
	"go.viam.com/utils/secrets"
)

func main() {
	utils.ContextualMain(mainWithArgs, logger)
}

var logger = golog.NewDevelopmentLogger("secrets")

// Arguments for the command.
type Arguments struct {
	SourceType string `flag:"0,required,usage=source type"`
	SecretName string `flag:"1,required,usage=secret name"`
}

func mainWithArgs(ctx context.Context, args []string, logger golog.Logger) error {
	var argsParsed Arguments
	if err := utils.ParseFlags(args, &argsParsed); err != nil {
		return err
	}

	source, err := secrets.NewSource(ctx, secrets.SourceType(argsParsed.SourceType))
	if err != nil {
		return err
	}

	value, err := source.Get(ctx, argsParsed.SecretName)
	if err != nil {
		return err
	}

	logger.Info(value)
	return nil
}
