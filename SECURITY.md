# Security Policy

vip-manager runs with enough privileges to add and remove addresses on a
network interface, it opens raw sockets to send gratuitous ARP messages, and it
holds credentials for the DCS and, with `manager-type=hetzner`, for the Hetzner
Robot API. Please treat findings in these areas as security relevant.

## Supported versions

Security fixes are released for the latest minor release. Older releases are
not maintained, please upgrade before reporting an issue that is already fixed
in the current version.

## Reporting a vulnerability

Please **do not open a public issue** for a vulnerability.

Use GitHub's private vulnerability reporting instead: go to the
[Security tab](https://github.com/cybertec-postgresql/vip-manager/security)
of this repository and choose *Report a vulnerability*. That creates a private
advisory only the maintainers can see.

Helpful in a report:

- the version of vip-manager and the `manager-type` and `dcs-type` in use
- what an attacker gains, and what access they need to get there
- the configuration needed to reproduce it, with credentials removed

You will get an acknowledgement of the report, and we will let you know when a
fix is released. If you would like to be credited in the advisory, say so in
the report.

## Out of scope

- vulnerabilities in etcd, consul, Patroni or PostgreSQL themselves - please
  report those to the respective project
- findings that require an attacker to already be root on the machine that
  runs vip-manager, since the daemon runs with those privileges anyway
