# Settings

The settings of HLab, stored in `settings.toml`. Each field says when a
change applies: the log level at once, the stop behaviour at the next
stop, the others at the next cloud start.

- **Save settings** checks every field, shows errors under it and saves
  nothing until all fields are valid.
- **Trust the HLab CA** adds the HLab CA, which signs managed
  certificates, to the trust stores of Windows and WSL2; switching it off
  removes it. Read the risk below the switch before you turn it on.
- **Save CA certificate** saves the CA certificate as `hlab-ca.pem`, for a
  client that should trust it without the switch.
- The switch and **Save CA certificate** need the running cloud.
