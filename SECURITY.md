# Security policy

Please report security problems to **security@waniis.com**. Don't open a public issue for them.

Include what you found, how to reproduce it, and the version (`waniis version`). We will answer
within a few working days and keep you posted until it is fixed. Please give us a reasonable time
to fix it before you share details publicly.

## What counts

- Tokens leaking: into output, logs, shell history or files other than the credential store.
- Credentials stored with the wrong permissions, or sent to a host other than the one they were
  issued for.
- TLS verification being skipped.
- A malicious server response making the CLI run commands or write outside the files you asked for.
- Exports (CSV, Excel) that run content when opened.

The CLI keeps credentials in your operating system's keychain, or in a file only you can read
(mode 0600) where there is no keychain. It never prints a token after you create it.

Only the latest release gets fixes.
