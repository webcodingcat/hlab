# Phase 30 manual checks

## REL-01 The repository webcodingcat/hlab holds the history of main under Apache-2.0, with LICENSE and README links to it

- Start: https://github.com/webcodingcat/hlab (signed in to GitHub)
- Setup: none; the phase close created the repository private and pushed `main`.
- Steps: 1. Open the repository page. 2. Check that it is marked "Private" and the default branch is `main`. 3. Open `LICENSE`. 4. On the README, click the licence badge and the link in the "Licence" section. 5. Compare the latest commit shown on GitHub with `git log --oneline -1 main` in your checkout.
- Expected: GitHub shows the licence as "Apache-2.0"; both README links open `LICENSE`; the latest commit on GitHub is the same as your local `main`. Making the repository public is your step, when you want it.
- Result: open

## REL-02 Every push to main and every pull request against main runs build, Go tests, lint, frontend tests and the installer build on a Windows runner

- Start: https://github.com/webcodingcat/hlab/actions/workflows/ci.yml
- Setup: none; the close pushed `main` and waited for the run.
- Steps: 1. Open the latest run of "CI" for `main`. 2. Look at its jobs.
- Expected: the run is green and has the five jobs build, test, lint, frontend and installer, each on `windows-latest`; no end-to-end tests run. The CI badge on the README shows "passing".
- Result: open

## REL-03 A tag vX.Y.Z builds and publishes the MSI, the setup EXE and SHA256SUMS.txt as a GitHub release

- Start: https://github.com/webcodingcat/hlab/blob/main/.github/workflows/release.yml and `docs/development/releasing.md`
- Setup: the first real release v0.1.0 is made by `/wcc:release`; until then this check covers the workflow and the documentation.
- Steps: 1. Open `release.yml` on GitHub and check that it starts on tags `vX.Y.Z`. 2. Read the "Installation" section of the README and the "Making a release" section of `docs/development/releasing.md`. 3. After `/wcc:release`: open the Releases page, download `hlab-setup-0.1.0.exe` and `SHA256SUMS.txt`, and run the README's `Get-FileHash` check.
- Expected: the workflow and both documents describe the same steps; after v0.1.0 the release holds `hlab-0.1.0.msi`, `hlab-setup-0.1.0.exe` and `SHA256SUMS.txt`, and the check prints True.
- Result: open
