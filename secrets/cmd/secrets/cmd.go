package main

import (
	"context"
	"errors"
	"flag"
	"log"

	"go.viam.com/utils/secrets"
)

func main() {
	err := realMain()
	if err != nil {
		log.Fatal(err)
	}
}

func realMain() error {
	flag.Parse()

	if flag.NArg() != 2 {
		return errors.New("need exactly 2 arguments, provider and secret")
	}

	ctx := context.Background()

	source, err := secrets.NewSecretSource(ctx, secrets.SecretSourceType(flag.Arg(0)))
	if err != nil {
		return err
	}

	value, err := source.Get(ctx, flag.Arg(1))
	if err != nil {
		return err
	}

	log.Println(value)
	return nil
}
