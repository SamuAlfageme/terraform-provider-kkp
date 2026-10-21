package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/provider"
)

// Set by goreleaser at build time.
var version = "dev"

//go:generate go tool tfplugindocs generate --provider-name kkp

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.opentofu.org/samualfageme/kkp",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
