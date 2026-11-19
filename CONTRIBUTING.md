# Contributing to vip-manager

Thank you for taking the time. vip-manager is a small daemon that moves a
virtual IP between the members of a Patroni cluster, so most changes touch code
that decides whether a machine may answer on an address. Please keep that in
mind: a change that leaves the address assigned on the wrong node is worse than
a missing feature.

## Before you start

For anything beyond a typo, please open an issue first and describe what you
are after. That saves you from writing a patch that goes in a direction the
maintainers would not take.

## Building and testing

```shell
go build ./...
go test ./...
```

The linter that runs in CI is [golangci-lint](https://golangci-lint.run/), with
the configuration in `.golangci.yml`:

```shell
golangci-lint run
```

Please run `gofmt` on everything you touch. The Windows specific files are
built with `GOOS=windows`, which is easy to forget on Linux:

```shell
GOOS=windows go build ./...
GOOS=linux go build ./...
```

Some tests start etcd or consul in a container through
[testcontainers](https://golang.testcontainers.org/) and are skipped when no
Docker daemon is available. The tests that add and remove addresses need root
and are skipped otherwise, CI runs them in a privileged container. There is
also an end to end test in `test/behaviour_test.sh` which needs root and a
local etcd.

## Pull requests

- one topic per pull request, it makes review and a later `git bisect` easier
- add a test for a fix, so that the bug cannot come back unnoticed
- update `README.md` when you add or change a configuration item
- the commit subject follows the convention used in the history:
  `[+]` for an addition, `[-]` for a fix or a removal, `[*]` for a change of
  existing behaviour and `[!]` for a refactoring or a breaking change,
  e.g. `[-] remove VIP when DCS becomes unreachable, closes #336`
- describe *why* the change is needed in the body of the commit message, the
  diff already says what it does

## Reporting bugs

Please include the version (`vip-manager --version`), the configuration with
the credentials removed, and the log around the moment things went wrong. If
the log is not telling enough, `--verbose` adds the caller and the retries of
the DCS client.

Security issues do not belong in the issue tracker, see [SECURITY.md](SECURITY.md).
