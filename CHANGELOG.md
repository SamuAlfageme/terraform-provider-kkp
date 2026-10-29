# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-10-29

### Fixed

- `kkp_cluster`: the `app.kubernetes.io/managed-by` label that project portals add no longer fails the apply with "Provider produced inconsistent result after apply"; it is ignored like the labels KKP manages.
- `kkp_cluster`: `node_ports_allowed_ip_ranges` takes the value KKP picks when unset (`0.0.0.0/0`) instead of failing the apply.
- `kkp_machine_deployment`: the node labels (`system/cluster`, `system/project`) and cloud tags KKP adds are ignored, so setting `node_labels` or `tags` no longer fails the apply and the next plan no longer tries to remove them.
- `kkp_machine_deployment`: `operating_system_profile` and the OpenStack `availability_zone` take the values KKP picks when unset (e.g. `osp-ubuntu`, the datacenter's zone) instead of showing a diff on the next plan. Changing `operating_system` without a profile picks the default for the new operating system.
- `kkp_machine_deployment`: Google Cloud and DigitalOcean tags keep their configured order; KKP returns them sorted.

## [0.1.0] - 2026-10-22

### Added

- Provider configuration with bearer token authentication (`token`, `token_file` or `KKP_TOKEN`), a custom CA bundle, retries on rate limiting and gateway errors, and a default `project_id` read from service account tokens.
- Resource `kkp_project`.
- Resource `kkp_ssh_key`.
- Resource `kkp_service_account` and `kkp_service_account_token`.
- Resource `kkp_project_member`.
- Resource `kkp_cluster` for OpenStack, AWS, Azure, Google Cloud, Hetzner and DigitalOcean, with presets, user or application credentials, SSH key assignment, in-place upgrades and cleanup options on destroy.
- Resource `kkp_machine_deployment` for OpenStack, AWS, Azure, Google Cloud, Hetzner and DigitalOcean, with Amazon Linux 2 on AWS, with autoscaling bounds, taints, operating system profiles and waiting for available replicas.
- Data sources `kkp_project`, `kkp_projects`, `kkp_datacenter`, `kkp_datacenters`, `kkp_kubernetes_versions`, `kkp_cluster` and `kkp_cluster_kubeconfig`. `kkp_datacenter(s)` report the cloud region and default images.
