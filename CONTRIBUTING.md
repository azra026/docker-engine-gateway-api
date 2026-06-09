# Contributing

Thanks for your interest in improving **Docker Engine API Gateway**! This
project welcomes bug reports, feature proposals, documentation fixes, and code
contributions from the community.

> **Security issues:** Do **not** file public issues or pull requests for
> security vulnerabilities. Follow the private process in [SECURITY.md](./SECURITY.md).

## Code of Conduct

Be respectful and constructive. Harassment, personal attacks, and
discriminatory language are not tolerated. Maintainers may remove comments,
commits, or contributors that violate this principle. See
[CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md) for the full policy.

## Development prerequisites

- **Go 1.23 or newer** ([install](https://go.dev/dl/)).
- A POSIX shell and `git`.
- (Optional, for end-to-end manual testing) a local Docker daemon with an
  accessible socket. The automated test suite does **not** require Docker.

This project uses **only the Go standard library**. Please do not introduce
third-party dependencies without prior discussion in an issue — keeping the
dependency tree empty is a core design goal.

## Getting started

1. **Fork** the repository on GitHub.
2. **Clone** your fork:
   ```bash
   git clone https://github.com/<your-username>/docker-engine-gateway-api.git
   cd docker-engine-gateway-api
   git remote add upstream https://github.com/azra026/docker-engine-gateway-api.git
   ```
3. **Create a branch** for your change:
   ```bash
   git checkout -b feature/short-description
   ```

## Making changes

Before opening a pull request, ensure all of the following pass locally:

```bash
gofmt -l .            # must print nothing (code is gofmt-clean)
go vet ./...          # must report no issues
go test ./... -race   # all tests must pass
go build ./...        # must compile
```

Guidelines:

- Keep changes focused; one logical change per pull request.
- Add or update tests for any behavior change. Security-sensitive code
  (token validation, header stripping, the Unix dialer) **must** be covered.
- Match the existing code style, comment density, and naming.
- Update `README.md` / docs when behavior or configuration changes.

## Commit messages & pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org/) (for
  example `feat: add auth throttling`, `fix: strip authorization header`,
  `docs: update deployment guide`).
- Reference related issues (e.g. `Fixes #12`).
- In the PR description, explain **what** changed and **why**, and note any
  security implications.
- Maintainers may request changes; please keep the discussion respectful and
  responsive.

## Copyright & Licensing (please read)

By submitting a contribution (a pull request, patch, or any other material) to
this project, **you agree to the following terms**:

1. **License of contribution.** You license your contribution to the project and
   to all recipients under the [Apache License, Version 2.0](./LICENSE)
   (inbound = outbound).

2. **Original work / right to submit.** You certify that the contribution is your
   original work, or that you otherwise have the right to submit it under the
   Apache 2.0 license, and that you are legally entitled to grant the rights
   below.

3. **Relicensing & dual-licensing grant.** You additionally grant **James Roi
   Dela Cruz** (the project maintainer and copyright steward), and their
   successors and assigns, a perpetual, worldwide, non-exclusive, royalty-free,
   irrevocable copyright license to reproduce, prepare derivative works of,
   publicly display, sublicense, **relicense, and distribute your contribution
   and derivative works under any license terms**, including proprietary or
   commercial licenses.

   This enables future **dual-licensing** and commercial/enterprise pathways for
   the project as a whole while keeping the open-source edition under Apache 2.0.
   You retain all other rights, title, and interest in your contribution.

If you are contributing on behalf of an employer or other entity, you confirm you
are authorized to grant these rights on its behalf.

If you do not agree to these terms, please do not submit a contribution.

## Questions

Open a (non-security) GitHub issue for questions, proposals, or discussion before
starting large pieces of work — it saves everyone time.
