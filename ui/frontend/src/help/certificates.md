# Certificates

The TLS certificates of this project, used by the https services of load
balancers. Certificates can only be managed while the cloud runs.

- **Add certificate** opens a form. Its **Type** chooses between two kinds:
  - An **uploaded certificate** takes the **Name**, the **Certificate**
    (PEM, the certificate first, then its chain) and the **Private key**
    (PEM). Domains, fingerprint and validity are read from the certificate;
    the key is never shown again.
  - A **managed certificate** takes the **Name** and the **Domain names**,
    one per line. Every domain must equal or lie below a primary DNS zone
    of this project (wildcards such as `*.lab.internal` included). HLab's
    own CA issues the certificate, not Let's Encrypt, and renews it before
    it expires; clients trust it after `hlab ca trust on`. A domain outside
    the project's zones fails with `dns_zone_not_found`, shown in the
    notification and on the certificate's page.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the certificate after a
  confirmation. A certificate a load balancer service uses cannot be
  deleted.
- Clicking a row opens the certificate's page. **Overview** shows the
  type, the status with the issuance error, the domains, the fingerprint,
  the validity and the load balancers that use it; **Labels** edits its
  labels.
- The actions menu (`…`) on the certificate's page offers **Rename**, **Retry** for a
  managed certificate whose issuance failed, for example after the
  missing zone was added, and **Delete**.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
