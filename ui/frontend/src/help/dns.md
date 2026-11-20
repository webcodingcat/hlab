# DNS

The DNS zones of this project with their records. HLab's DNS server in the
appliance answers for them; the host, WSL2 and the servers ask it. No
registrar or public nameserver is involved. Zones can only be managed while
the cloud runs.

- **Add zone** opens a form for the **Domain** (a registrable domain such
  as `example.com`, or one label below `internal`, `test` or `home.arpa`
  such as `lab.internal`), the **Type** (an empty zone, a zone from an
  imported zone file, or a secondary zone with its primary nameservers),
  the default **TTL** and labels. A zone name is unique in the whole cloud.
- A secondary zone holds no records in HLab: its queries go to the host's
  DNS servers.
- The search field filters by name, or with `=` by labels, for example
  `team=ops`.
- **Delete** in the row menu (`…`) deletes the zone with all its records
  after you typed its name.
- Clicking a row opens the zone's page. **Overview** shows the mode, the
  nameservers, the default TTL and the number of records; **Labels** edits
  its labels; **Protection** turns the delete protection on or off.
- **Records** lists the record sets of a primary zone with name, type, TTL
  and values. **Add record set** asks for the name (`@` is the zone itself,
  `*` a wildcard), the type, an optional TTL (empty uses the zone's
  default), the values one per line and labels. The row menu (`…`) of a
  record set offers **Change TTL**, **Add records**, **Remove records**,
  **Change labels**, **Change protection** (a protected record set refuses
  every change of its records and TTL) and **Delete**.
- The SOA record set is managed by HLab, which raises its serial with every
  change; only its TTL can be changed.
- **Zone file** shows the export of a primary zone in BIND format with
  **Copy zone file**; **Import zone file** below replaces all records with
  the pasted zone file after a confirmation. A bad zone file is refused
  with its line number under the field.
- **Primary nameservers** of a secondary zone lists address, port and an
  optional TSIG algorithm and key per nameserver; **Save primary
  nameservers** replaces the list. HLab never transfers the zone from them.
- The actions menu (`…`) on the zone's page offers **Change default TTL**,
  **Import zone file** (a zone file in BIND format replaces all records),
  **Change protection** and **Delete**.

To switch to another section of the project, use the sidebar on the left;
**API tokens** manages the project's tokens.
