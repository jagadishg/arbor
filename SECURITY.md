# Security policy

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability involving credential exposure, request smuggling, unsafe file handling, or arbitrary code execution.

Use GitHub's private vulnerability reporting for this repository. Include the affected version, operating system, reproduction steps, impact, and any suggested mitigation. Reports will be acknowledged as soon as practical.

## Supported versions

Until Arbor reaches a stable release, security fixes are made against the latest tagged release and the `main` branch.

## Secret-handling expectations

Arbor supports `env://` and `keychain://` references so secret values do not need to be committed. Users remain responsible for excluding any files that directly contain credentials and for reviewing request definitions before sharing execution output.
