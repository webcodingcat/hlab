# The catalog

What the lab offers and how the user changes it. How to update it from
Hetzner's API is a recipe in DEVELOPING.md.

## The catalog

`internal/catalog` holds what Hetzner offers: locations, server types, load balancer
types, Storage Box types, prices, system images and ISOs. Each entry has Hetzner's
real ID and name, because the tools check them (`compat/requirements.md`).

- **Built in, and the user can change it.** The built-in catalog is
  `internal/catalog/catalog.yaml`, compiled in with `go:embed`. `catalog.Load` then
  reads a `catalog.yaml` from the data folder on top of it. Each key in the user's
  file replaces the built-in value; a list is replaced as a whole. The start stops,
  naming the file, on unknown keys, duplicate IDs or names, values the API does not
  allow, and unknown locations.
- **Hetzner's shape.** The keys are those of Hetzner's JSON, so answers of the real
  API can be copied into the catalog (DEVELOPING.md, Recipes). The differences:
  - a server type's locations give only the location's name; the ID comes from
    `locations`;
  - an ISO without `architecture` fits every architecture;
  - images are system images, always `available`, without labels.

  Prices keep all decimals, as `GET /pricing` shows them. `GET /load_balancer_types`
  rounds them to four decimals, as Hetzner does.
- **Only what the lab can run:**
  - x86 server types and ISOs, because Hyper-V on an x86 host runs no ARM guests;
  - each system image only in its newest version (Debian 13, Ubuntu 26.04).

  A server type is `available` wherever it is not deprecated. On Hetzner the flag
  also reflects current capacity, which the lab does not copy.
- **The lab owns it.** `lab.Options.Catalog` takes a loaded catalog (nil means the
  built-in one), and `Lab.Catalog()` returns it. It does not change while the lab
  runs, so reading it needs no lock. Lists are sorted by ID, as Hetzner sorts them.
