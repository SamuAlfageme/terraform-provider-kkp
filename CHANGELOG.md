# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Provider configuration with bearer token authentication (`token`, `token_file` or `KKP_TOKEN`), a custom CA bundle, retries on rate limiting and gateway errors, and a default `project_id` read from service account tokens.
- Resource `kkp_project`.
- Resource `kkp_ssh_key`.
- Resource `kkp_service_account` and `kkp_service_account_token`.
- Resource `kkp_project_member`.
- Resource `kkp_cluster` for OpenStack, AWS, Azure, Google Cloud, Hetzner and DigitalOcean, with presets, user or application credentials, SSH key assignment, in-place upgrades and cleanup options on destroy.
- Resource `kkp_machine_deployment` for OpenStack, AWS, Azure, Google Cloud, Hetzner and DigitalOcean, with Amazon Linux 2 on AWS, with autoscaling bounds, taints, operating system profiles and waiting for available replicas.
- Data sources `kkp_project`, `kkp_projects`, `kkp_datacenter`, `kkp_datacenters`, `kkp_kubernetes_versions`, `kkp_cluster` and `kkp_cluster_kubeconfig`. `kkp_datacenter(s)` report the cloud region and default images.
