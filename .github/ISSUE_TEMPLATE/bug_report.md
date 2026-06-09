---
name: Bug report
about: Report a (non-security) bug
title: "fix: "
labels: bug
---

> ⚠️ **Do not report security vulnerabilities here.** Use the private process in
> [SECURITY.md](../../SECURITY.md).

**Describe the bug**
A clear and concise description of what the bug is.

**To reproduce**
Steps / commands (redact your token):

```
GATEWAY_AUTH_TOKEN=*** ./docker-engine-gateway-api
curl ...
```

**Expected behavior**
What you expected to happen.

**Actual behavior**
What actually happened (include relevant JSON log lines — they never contain the token).

**Environment**
- Gateway version (`docker-engine-gateway-api -version`):
- Go version (if building from source):
- OS / architecture:
- Docker Engine version:
- Deployment (binary / Docker / Compose / k8s):

**Additional context**
Anything else that helps.
