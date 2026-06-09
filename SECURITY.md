# Security Policy

The Docker Engine API Gateway is a security-sensitive component — it guards
access to a root-equivalent interface. We take vulnerabilities seriously and
appreciate responsible, coordinated disclosure.

## Supported versions

This project is pre-1.0 and ships from `main`. Security fixes are applied to the
latest release and the `main` branch.

| Version        | Supported          |
| -------------- | ------------------ |
| `main` (latest)| :white_check_mark: |
| older tags     | :x:                |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
pull requests, or discussions.** Public disclosure before a fix is available puts
all users at risk.

Instead, report privately using **either** of the following:

1. **GitHub Security Advisories (preferred).**
   Go to the repository's **Security** tab → **Report a vulnerability**, or visit:
   `https://github.com/azra026/docker-engine-gateway-api/security/advisories/new`
   This opens a private channel visible only to you and the maintainers.

2. **Email.** Send details to the maintainer at **`dcjr026@gmail.com`**
   If possible, encrypt sensitive details; request the maintainer's PGP key in an
   initial low-detail message.

Please include, as much as you can:

- A description of the vulnerability and its impact.
- Steps to reproduce, or a proof-of-concept.
- Affected version / commit, configuration, and environment.
- Any suggested remediation.

## What to expect

- **Acknowledgement** of your report within **3 business days**.
- An initial **assessment and severity triage** within **7 business days**.
- Regular updates on remediation progress.
- **Coordinated disclosure:** we will work with you to agree on a disclosure
  timeline (target: within **90 days**, or sooner once a fix ships). We will
  credit you in the advisory and release notes unless you prefer to remain
  anonymous.

## Scope

In scope:

- Authentication bypass (reaching the proxied Docker API without a valid token).
- Token leakage (e.g. the operator token reaching the daemon or logs).
- Timing or side-channel weaknesses in token validation.
- Request smuggling, header injection, or SSRF via the proxy.
- Denial of service against the gateway process.

Out of scope:

- The inherent risk of exposing the Docker socket itself — this is documented in
  the [README security warning](./README.md#️-security-warning--read-this-first).
  This gateway provides authentication, not authorization/sandboxing of what an
  authenticated caller can do.
- Misconfiguration by the operator (e.g. running without TLS, using a weak token,
  granting the socket to untrusted users).

Thank you for helping keep the project and its users safe.
