# Local plugin install path for `tofu`/`terraform` without dev_overrides.
HOSTNAME  ?= registry.opentofu.org
NAMESPACE ?= samualfageme
NAME      ?= kkp
VERSION   ?= 0.0.0-dev
OS_ARCH   := $(shell go env GOOS)_$(shell go env GOARCH)
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/$(HOSTNAME)/$(NAMESPACE)/$(NAME)/$(VERSION)/$(OS_ARCH)

default: build

.PHONY: build
build:
	go build -o terraform-provider-$(NAME) .

.PHONY: install
install: build
	mkdir -p $(PLUGIN_DIR)
	cp terraform-provider-$(NAME) $(PLUGIN_DIR)/terraform-provider-$(NAME)_v$(VERSION)

.PHONY: lint
lint:
	golangci-lint run

.PHONY: fmt
fmt:
	gofmt -s -w .
	tofu fmt -recursive examples

.PHONY: generate
generate:
	go generate ./...

# Unit tests run the provider through OpenTofu against an in-memory fake API.
.PHONY: test
test:
	TF_ACC_TERRAFORM_PATH=$$(which tofu) \
	TF_ACC_PROVIDER_HOST=registry.opentofu.org \
	TF_ACC_PROVIDER_NAMESPACE=$(NAMESPACE) \
	go test -cover -timeout 10m ./...

# Same tests driven by the Terraform CLI.
.PHONY: test-terraform
test-terraform:
	go test -cover -timeout 10m ./...

# Like `generate`, but exports the schema with OpenTofu instead of letting
# tfplugindocs download Terraform (for machines that cannot reach HashiCorp).
.PHONY: generate-tofu
generate-tofu:
	@tmp=$$(mktemp -d) && \
	go build -o $$tmp/plugins/terraform-provider-kkp . && \
	printf 'provider_installation {\n  dev_overrides { "registry.terraform.io/hashicorp/kkp" = "%s" }\n  direct {}\n}\n' $$tmp/plugins > $$tmp/rc && \
	printf 'terraform {\n  required_providers {\n    kkp = { source = "registry.terraform.io/hashicorp/kkp" }\n  }\n}\n' > $$tmp/main.tf && \
	(cd $$tmp && TF_CLI_CONFIG_FILE=$$tmp/rc tofu providers schema -json > schema.json) && \
	go tool tfplugindocs generate --provider-name kkp --providers-schema $$tmp/schema.json && \
	rm -rf $$tmp
