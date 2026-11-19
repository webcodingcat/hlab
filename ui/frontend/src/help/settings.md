# Settings

The settings of the cloud, stored in `settings.toml`. Each field says when
a change applies; some apply only at the next cloud start.

- **Save settings** checks every field and shows errors under it.
- **Trust the hlab CA** adds the hlab certificate authority to the Windows
  trust store, so tools accept the API's certificate.
