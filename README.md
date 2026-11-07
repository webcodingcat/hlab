# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet":
a service that answers like Hetzner's APIs, with servers that are real VMs. Tools
such as OpenTofu (hcloud provider), the hcloud CLI and Ansible are meant to work
against it without changes.

**Status:** being rebuilt in Go, phase 1a (see [docs/roadmap.md](docs/roadmap.md)).
Nothing is usable yet; the earlier implementation (Docker and QEMU) is on the branch
`legacy`.

## Compatibility

- Windows 11 with Hyper-V, used from WSL2 (Ubuntu) or Windows.
- Linux follows in a later phase.

## Development

```bash
bin/dev test
```

[DEVELOPING.md](DEVELOPING.md) explains the code and the tools, [STYLE.md](STYLE.md)
the standards.
