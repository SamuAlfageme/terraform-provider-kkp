# AGENTS.md

Instructions for AI coding agents (and humans) working on this repository.

## What this is

An OpenTofu/Terraform provider for the [Kubermatic Kubernetes Platform](https://www.kubermatic.com/products/kubermatic-kubernetes-platform/) REST API (the API behind the KKP dashboard). It is written in Go with the [terraform-plugin-framework](https://developer.hashicorp.com/terraform/plugin/framework) and protocol v6. The registry address is `samualfageme/kkp`.

API reference: the swagger.json of the KKP API (`modules/api/cmd/kubermatic-api/swagger.json` in [kubermatic/dashboard](https://github.com/kubermatic/dashboard)). When a response shape matters, check the handlers in `modules/api/pkg/handler` too: the real server differs from the spec in places.

## Layout

```
main.go                  provider entrypoint; `go generate` builds the docs
internal/client/         KKP HTTP client (bearer auth, retries, errors); endpoints.go has one method per call
internal/kkpmock/        in-memory fake KKP API used by all provider tests
internal/provider/       provider, resources (resource_*.go), data sources (data_sources.go)
examples/                HCL snippets embedded in the docs (the paths are a tfplugindocs convention)
templates/               doc templates
docs/                    GENERATED: never edit by hand
```

## Commands

```shell
make build
make test            # all tests through OpenTofu (requires `tofu` on PATH)
make test-terraform
make lint            # golangci-lint v2
make generate        # regenerate docs/ after ANY schema, example or template change
make generate-tofu   # the same, exporting the schema with OpenTofu (no Terraform download)
tofu fmt -recursive examples
```

Before you finish a change, run `go vet ./... && make lint`, `make test` and `make generate`, and commit the `docs/` changes. CI fails if `docs/` is stale.

## KKP API quirks to remember

These are mirrored in `internal/kkpmock`; keep the fake in sync when you add an endpoint.

- Auth is `Authorization: Bearer <token>`. Service account tokens are JWTs whose `project_id` claim names their project. They cannot create projects (403 "the Service Account is not allowed to create a project") and only see their own project.
- Empty lists come back as `null`, not `[]`.
- Projects are created `Inactive` and become `Active` shortly after. Project update is a PUT of the whole object.
- There is no GET for a single SSH key, service account or token: list and filter.
- Group names are sent as `editors`/`viewers`/`projectmanagers`/`owners` and come back suffixed with `-<project id>`.
- Cluster and machine deployment PATCH is a JSON merge patch (`application/merge-patch+json`). Maps merge, so removed keys must be sent as `null`; arrays are replaced.
- Cluster PATCH copies only some fields from the patched object (see `PatchEndpoint` in `handler/common/cluster.go`: name, labels, annotations, cloud, version, audit logging, admission plugins, OPA, MLA, CNI, expose strategy, API server allowed IPs, Kubernetes dashboard, Kyverno, …). Everything else, e.g. the datacenter, pod/service CIDRs or `enableUserSSHKeyAgent`, is silently dropped, so those attributes use `RequiresReplace`.
- Cluster responses never contain cloud credentials (they live in a seed secret; `credentialsReference` is returned instead). Keep credentials from state.
- Cluster health values are `HealthStatusUp`/`Down`/`Provisioning`. "Ready" means what `ExtendedClusterHealth.AllHealthy()` checks.
- Creating a machine deployment answers 503 "Cluster components are not ready yet" until the cluster is healthy: retry.
- Machine deployment ID is its name. The kubelet version defaults to the cluster version, must not be newer than the control plane, and may lag at most three minors. The control plane upgrades one minor at a time, and KKP refuses an upgrade that leaves kubelets out of skew.
- In a machine deployment PATCH, an operating system replaces the old one instead of merging with it.
- Cluster deletion takes `DeleteVolumes` and `DeleteLoadBalancers` request headers; deletion is asynchronous (wait for 404).
- The cluster SSH key detach route is `/api/v2/projects/{p}/clusters/{c}/sshkeys/{key}`; swagger.json lists it without `/v2`.
- `GET /api/v2/projects/{p}/clusters` only fills `machineDeploymentCount` with `?show_dm_count=true`.

## Conventions

- **Client first.** Add API calls to `internal/client/endpoints.go` as typed methods.
- **Resources:** `id` is the KKP ID. Project-scoped resources have an Optional+Computed `project_id` (`RequiresReplace`) that defaults to the provider's project, and import as `<project_id>/<id>` (`<project_id>/<cluster_id>/<id>` for machine deployments). Attributes KKP defaults are Optional+Computed with `UseStateForUnknown`. Status values are Computed only. A 404 on read means `resp.State.RemoveResource`. Every attribute needs a `MarkdownDescription`.
- **Tests:** every resource and data source needs `resource.UnitTest` coverage against `kkpmock`: create, update, import and destroy. Tests never need real credentials.
- **Docs:** for each new resource, add `examples/resources/<type>/resource.tf` and `import.sh`, then run `make generate`.
- **Changelog:** add an entry under `## [Unreleased]` in `CHANGELOG.md` for every user-facing change.
- **Commits:** Conventional Commits (`feat:`, `fix:`, `docs:`, `ci:`, `chore:`).
- **Secrets:** never commit tokens or `.tfstate`. Use `KKP_*` environment variables.

## Roadmap

More cloud providers for clusters and machine deployments (the API shapes are in swagger.json under `CloudSpec` and `NodeCloudSpec`), cluster addons and applications, and group project bindings.

## Releasing

Tag `vX.Y.Z` on `main` after moving the `Unreleased` changelog entries under that version. `.github/workflows/release.yml` runs GoReleaser with GPG signing. The secrets `GPG_PRIVATE_KEY` and `PASSPHRASE` must exist.
