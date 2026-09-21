# Changelog

Notable changes to OpenTacit. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This project versions three types of output:

- **The binary and its CLI.** Semver as usual.
- **The `pkg/` packages.** At `v0` the Go API may break in a minor release. It
  is published because the wire contracts need a reference implementation, not
  because it is stable yet.
- **The wire contracts:** the JSON Schemas under `schemas/`, the MCP tool
  names, the `tacit-feed/v1` protocol. These version independently of the
  binary and are the thing other implementations may rely on. A breaking change
  to any of them gets its own version, and is listed here under **Changed**
  whether or not the binary's version moved.

## [Unreleased]

Nothing released yet. The first tag will be `v0.1.0`.
