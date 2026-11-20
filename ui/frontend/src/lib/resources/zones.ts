// DNS zone actions: the create form's request, the detail page's actions
// menu and the actions of the detail tabs and list row menus, the RRSet
// entries included. RRSet entries take input.rr_name and input.rr_type and
// address /zones/{id}/rrsets/{rr_name}/{rr_type}.
import {
  changeProtection,
  createAt,
  pick,
  postAction,
  remove,
  type Input,
  type ResourceAction,
} from "../resourceActions";

const base = "zones";

/**
 * create sends name, mode and labels; a primary zone optionally its default
 * TTL or a zone file to import, a secondary zone its primary nameservers.
 */
export const create: ResourceAction = createAt(base, "Add zone", (i) =>
  pick(i, ["name", "mode", "ttl", "zonefile", "primary_nameservers", "labels"]),
);

const changeTTL = postAction(base, "change_ttl", "change_ttl", {
  label: "Change default TTL",
  body: (i) => ({ ttl: i.ttl }),
  needsInput: true,
});

const importZonefile = postAction(base, "import_zonefile", "import_zonefile", {
  label: "Import zone file",
  body: (i) => ({ zonefile: i.zonefile }),
  confirm: "plain",
  needsInput: true,
});

const del = remove(base, "Delete", "typed-name");

/** menu is the detail page's actions menu, Delete last. */
export const menu: ResourceAction[] = [changeTTL, importZonefile, changeProtection(base), del];

/** rrsetPath is the path of the RRSet input.rr_name/input.rr_type of zone id. */
const rrsetPath = (id: number, i: Input) => `/${base}/${id}/rrsets/${i.rr_name}/${i.rr_type}`;

/** rrsetAction is a POST to an RRSet's /actions/<act>. */
function rrsetAction(act: string, label: string, body: ResourceAction["body"], confirm: "none" | "plain" = "none"): ResourceAction {
  return {
    id: `rrset_${act}`,
    label,
    method: "POST",
    path: (id, i) => `${rrsetPath(id, i)}/actions/${act}`,
    body,
    confirm,
    needsInput: true,
  };
}

/** tabs are the actions of the detail tabs and the list row menu. */
export const tabs: Record<string, ResourceAction> = {
  change_primary_nameservers: postAction(base, "change_primary_nameservers", "change_primary_nameservers", {
    label: "Change primary nameservers",
    body: (i) => ({ primary_nameservers: i.primary_nameservers }),
    needsInput: true,
  }),
  export_zonefile: {
    id: "export_zonefile",
    label: "Export zone file",
    method: "GET",
    path: (id) => `/${base}/${id}/zonefile`,
    confirm: "none",
    needsInput: false,
  },
  create_rrset: {
    id: "create_rrset",
    label: "Add record set",
    method: "POST",
    path: (id) => `/${base}/${id}/rrsets`,
    body: (i) => pick(i, ["name", "type", "ttl", "labels", "records"]),
    confirm: "none",
    needsInput: true,
  },
  rrset_change_ttl: rrsetAction("change_ttl", "Change TTL", (i) => ({ ttl: i.ttl })),
  rrset_add_records: rrsetAction("add_records", "Add records", (i) => ({ records: i.records })),
  rrset_remove_records: rrsetAction("remove_records", "Remove records", (i) => ({ records: i.records }), "plain"),
  rrset_labels: {
    id: "rrset_labels",
    label: "Change labels",
    method: "PUT",
    path: rrsetPath,
    body: (i) => ({ labels: i.labels }),
    confirm: "none",
    needsInput: true,
  },
  rrset_change_protection: rrsetAction("change_protection", "Change protection", (i) => ({ change: i.change })),
  rrset_delete: {
    id: "rrset_delete",
    label: "Delete",
    method: "DELETE",
    path: rrsetPath,
    confirm: "plain",
    needsInput: false,
  },
  delete: del,
};
