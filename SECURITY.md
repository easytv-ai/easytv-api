# Security Policy

We take the security of EasyTV seriously. Thank you for helping keep the
project and its users safe.

## Supported Versions

EasyTV is pre-1.0. Only the latest commit on the default branch of each
repository receives security fixes.

| Version | Supported |
| ------- | --------- |
| default branch (`main`) | ✅ |
| older commits / forks | ❌ |

## Reporting a Vulnerability

**Please do not open a public issue for security problems.**

Use GitHub's private vulnerability reporting instead:

1. Open the repository on GitHub.
2. Go to **Security → Advisories → Report a vulnerability**.
3. Describe the issue, affected component, reproduction steps and impact.

You will get an acknowledgement within **3 business days** and a status update
at least every **7 days** until the report is resolved.

If you cannot use GitHub, email the maintainers at **leida@easyapi.com** with
`[SECURITY] easytv-api` in the subject line.

## What to Include

- The repository and commit / version you tested
- A minimal proof of concept or reproduction steps
- The impact you believe the issue has (data exposure, privilege escalation,
  RCE, …)
- Any suggested mitigation

## Disclosure Process

1. We confirm the report and assess severity (CVSS v3.1).
2. We develop and test a fix on a private branch.
3. We publish the fix, a GitHub Security Advisory and a `CHANGELOG.md` entry.
4. We credit the reporter unless they prefer to stay anonymous.

We ask reporters to give us up to **90 days** before public disclosure.

## Scope

In scope: everything in this repository, including the Docker images and CI
workflows it ships.

Out of scope: the hosted service at easytv.ai (report issues there through the
same channel, but SLAs above apply to the open-source code only), social
engineering, denial of service against third-party infrastructure.

## Security Best Practices for Self-Hosters

- Never commit `.env` files. Only `.env.example` belongs in git.
- Rotate `CRYPTO_SECRET`, database and object-storage credentials when a
  deployment is shared or leaves your control.
- Run the API behind TLS and keep `DEBUG=false` in production.
- Keep dependencies up to date; Dependabot is enabled on every repository.
