# Releasing

HLab lives at <https://github.com/webcodingcat/hlab>. GitHub Actions
checks every change to `main` and publishes a release for every version
tag. Both workflows run on GitHub-hosted `windows-latest` runners and use
the tool versions of [Developer setup](setup.md#tools).

## CI

The workflow `CI` ([.github/workflows/ci.yml](../../.github/workflows/ci.yml))
runs on every push to `main` and on every pull request against `main`. Its
five jobs run in parallel:

| Job | Runs |
| --- | --- |
| `build` | `task build` |
| `test` | `task openapi`, then `task test` |
| `lint` | `task lint` with golangci-lint v2.15.0 |
| `frontend` | `task test:frontend` |
| `installer` | `task installer VERSION=0.0.0` with WiX 7.0.1 and its util and bootstrapper extensions |

The `installer` job only proves that the installers build; it keeps no
download. The end-to-end tests (`task e2e`) and the elevated installer
suite (`task e2e:elevated`) need Hyper-V and administrator rights and run
only on your machine; see [Build and test](build-and-test.md).

The jobs cache Go modules, the Go build and npm packages. The `test` job
also caches the two OpenAPI descriptions, keyed on the lock files, so it
downloads them again only after a re-pin or when the cache has expired.

Read the result of the last run on `main`, then the details of one run:

```
gh run list --branch main --limit 1
gh run view <run id>
gh run view <run id> --log-failed
```

`gh run view` lists the jobs by the names in the table; `--log-failed`
prints the log of the failed steps only.

### Minutes

The repository is private, so CI uses GitHub's free allowance for private
repositories: 12 USD of runner time a month, about 1,200 minutes on
Windows runners. A new push to a pull request cancels the running CI of
that pull request, which then shows as cancelled. A run on `main` is never
interrupted; a newer push to `main` waits until it has finished.

## OpenAPI pins

The API tests validate every response against Hetzner's published OpenAPI
descriptions. `task openapi` downloads them and accepts only the version
whose SHA-256 is pinned in the repository:

| Lock file | Description | Cached as |
| --- | --- | --- |
| `api/openapi.lock` | Cloud API | `%LocalAppData%\hlab\openapi\cloud.spec.json` |
| `api/hetzner-openapi.lock` | Hetzner API (storage boxes) | `%LocalAppData%\hlab\openapi\hetzner.spec.json` |

A lock file holds the URL and the SHA-256:

```json
{
  "url": "https://docs.hetzner.cloud/cloud.spec.json",
  "sha256": "940bd801a3df67e48badbec6eb82f1dc8e2ad7871093a46bf25b0e313d11192a"
}
```

Hetzner publishes each description at a fixed URL and changes it without
a new address. When the published file no longer matches its pin,
`task openapi` caches nothing and stops:

```
openapi: cloud.spec.json: Hetzner has changed the published description at https://docs.hetzner.cloud/cloud.spec.json; the pin in api/openapi.lock no longer matches (published SHA-256 <hash>, pinned SHA-256 <hash>); a deliberate re-pin is needed: update the lock file and run task openapi (see docs/development/releasing.md)
```

On your machine the cached copy keeps the tests running. In CI the `test`
job turns red as soon as its cache is gone. Re-pin in a commit of its own:

1. Copy the cached description (for example `cloud.spec.json`) out of
   `%LocalAppData%\hlab\openapi\` as the old version.
2. Write the published SHA-256 from the message into the lock file.
3. Run `task openapi`; it prints `cached <path>` for both descriptions.
4. Run `task test`.
5. Compare the old and the new description for the paths HLab serves:
   added or removed operations, required fields, enums and status codes.
   Fix what fails in the code; change a test only where it contradicts the
   new description. When HLab now differs from Hetzner in a new way,
   update [API coverage](api-coverage.md) and
   [Known deviations](../deviations.md).

## Making a release

A release is a tag `vX.Y.Z` on a commit of `main` whose CI run is green.
The version must be valid for an MSI: major and minor at most 255, patch
at most 65535.

1. Check the CI of the commit:

   ```
   gh run list --commit <sha> --workflow CI
   ```

2. Tag the commit and push the tag:

   ```
   git tag v0.1.0 <sha>
   git push origin v0.1.0
   ```

The pushed tag starts the workflow `Release`
([.github/workflows/release.yml](../../.github/workflows/release.yml)). It
runs no tests. It stops before building when the tag is no valid MSI
version, then runs `task installer VERSION=X.Y.Z`, writes
`SHA256SUMS.txt` and publishes the GitHub release `vX.Y.Z` with generated
release notes and three files:

- `hlab-X.Y.Z.msi`
- `hlab-setup-X.Y.Z.exe`
- `SHA256SUMS.txt`, one line per installer: the lowercase SHA-256, two
  spaces and the file name.

Check the release and its files:

```
gh release view v0.1.0
```

After downloading the three files into one folder, check them in WSL2 or
Git Bash:

```
> sha256sum -c SHA256SUMS.txt
hlab-0.1.0.msi: OK
hlab-setup-0.1.0.exe: OK
```

Users check the setup program with `Get-FileHash`, as the
[README](../../README.md#installation) shows. The installers are not
signed, so Windows SmartScreen warns about the setup program.

To build the installers on your own machine, see
[Installers](build-and-test.md#installers).

## Secret scan

The history of `main` is pushed as it is, so scan it with
[gitleaks](https://github.com/gitleaks/gitleaks) before every push
(`winget install --id Gitleaks.Gitleaks -e`):

```
> gitleaks git --redact --no-banner
INF 1326 commits scanned.
INF scanned ~9162557 bytes (9.16 MB) in 2.28s
INF no leaks found
```

`--redact` keeps the values out of the report. Push only when it says
`no leaks found`. A finding names the file, commit and rule; a real token,
password or key must not be pushed.

[.gitleaksignore](../../.gitleaksignore) lists the findings that are no
secrets, by gitleaks fingerprint, each with a comment line naming the file
and the reason: example tokens and passwords of the local emulator in the
user guide, password and key fixtures of unit tests, and a code comment
naming hash formats. Add a fingerprint there only for a value that is
provably not a secret.
